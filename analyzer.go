package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "sort"
    "sync"
    "time"
    "github.com/gorilla/websocket"
)

var matchData = map[string]map[string]string{
    "Sansabet": make(map[string]string),
    "Pinnacle": make(map[string]string),
}

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
}

const MARGIN = 1.08

var extraPercents = []struct {
    Min, Max, ExtraPercent float64
}{
    {2.29, 2.75, 1.03},
    {2.75, 3.2, 1.04},
    {3.2, 3.7, 1.05},
}

// Вспомогательная функция для расчёта extra_percent
func getExtraPercent(pinnacleOdd float64) float64 {
    for _, ep := range extraPercents {
        if pinnacleOdd >= ep.Min && pinnacleOdd < ep.Max {
            return ep.ExtraPercent
        }
    }
    return 1.0
}

// Функция для расчёта ROI
func calculateROI(sansaOdd, pinnacleOdd float64) float64 {
    extraPercent := getExtraPercent(pinnacleOdd)
    roi := (sansaOdd/(pinnacleOdd*MARGIN*extraPercent) - 1 - 0.03) * 100 * 0.67
    return roi
}

// Полный анализ исходов
func findCommonOutcomes(sansabetData, pinnacleData string) map[string][2]float64 {
    log.Printf("[DEBUG] Входные данные для анализа: Sansabet=%s, Pinnacle=%s", sansabetData, pinnacleData)

    var sansabetOdds, pinnacleOdds struct {
        Win1x2 map[string]float64 `json:"Win1x2"`
        Totals map[string]struct {
            WinMore float64 `json:"WinMore"`
            WinLess float64 `json:"WinLess"`
        } `json:"Totals"`
    }
    common := make(map[string][2]float64)

    if err := json.Unmarshal([]byte(sansabetData), &sansabetOdds); err != nil {
        log.Printf("[ERROR] Ошибка парсинга данных Sansabet: %v", err)
        return common
    }
    if err := json.Unmarshal([]byte(pinnacleData), &pinnacleOdds); err != nil {
        log.Printf("[ERROR] Ошибка парсинга данных Pinnacle: %v", err)
        return common
    }

    for key, sansabetValue := range sansabetOdds.Win1x2 {
        if pinnacleValue, exists := pinnacleOdds.Win1x2[key]; exists && pinnacleValue >= 1.2 && pinnacleValue <= 3.5 {
            common[key] = [2]float64{sansabetValue, pinnacleValue}
            log.Printf("[DEBUG] Найден общий исход Win1x2: %s", key)
        }
    }

    for key, sansabetTotal := range sansabetOdds.Totals {
        if pinnacleTotal, exists := pinnacleOdds.Totals[key]; exists && pinnacleTotal.WinMore >= 1.2 && pinnacleTotal.WinMore <= 3.5 {
            common["Total "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
            log.Printf("[DEBUG] Найден общий исход Totals: %s", key)
        }
    }

    log.Printf("[DEBUG] Общие исходы: %+v", common)
    return common
}

// Полный расчет ROI
func calculateAndFilterCommonOutcomes(matchName string, commonOutcomes map[string][2]float64) []string {
    filtered := []struct {
        Outcome  string
        ROI      float64
        Sansa    float64
        Pinnacle float64
    }{}

    for outcome, values := range commonOutcomes {
        roi := calculateROI(values[0], values[1])
        if roi > -30 {
            filtered = append(filtered, struct {
                Outcome  string
                ROI      float64
                Sansa    float64
                Pinnacle float64
            }{outcome, roi, values[0], values[1]})
        }
    }

    sort.Slice(filtered, func(i, j int) bool {
        return filtered[i].ROI > filtered[j].ROI
    })

    results := []string{}
    for _, item := range filtered {
        results = append(results, fmt.Sprintf("%s | Исход: %s | ROI: %.2f%% | Sansabet: %.2f | Pinnacle: %.2f",
            matchName, item.Outcome, item.ROI, item.Sansa, item.Pinnacle))
    }

    return results
}

var matchPairs []MatchPair
var pairsMutex sync.Mutex

var sansabetConnection *websocket.Conn
var pinnacleConnection *websocket.Conn

var frontendClients = make(map[*websocket.Conn]bool)
var frontendMutex = sync.Mutex{}
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// Запуск анализатора
func startAnalyzer() {
    log.Println("[DEBUG] Анализатор запущен")
    go connectParsers()
    go processPairs()
    go startFrontendServer()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения мэтчинга: %v", err)
            return
        }
        log.Printf("[DEBUG] Новое подключение к мэтчингу")
        defer conn.Close()

        for {
            _, msg, err := conn.ReadMessage()
            log.Printf("[DEBUG] Сообщение от мэтчинга: %s", string(msg))
            if err != nil {
                log.Printf("[ERROR] Ошибка чтения от мэтчинга: %v", err)
                break
            }
            log.Printf("[DEBUG] Полученные данные от мэтчинга: %s", string(msg))

            var pairs []MatchPair
            if err := json.Unmarshal(msg, &pairs); err != nil {
                log.Printf("[ERROR] Ошибка парсинга данных мэтчинга: %v", err)
                continue
            }

            pairsMutex.Lock()
            matchPairs = pairs
            log.Printf("[DEBUG] matchPairs обновлён от мэтчинга: %+v", matchPairs)
            log.Printf("[DEBUG] Обновлённые matchPairs: %+v", matchPairs)
            pairsMutex.Unlock()
        }
    })

    log.Fatal(http.ListenAndServe(":7400", nil))
}

func connectParsers() {
    go connectToParser("ws://localhost:7100", &sansabetConnection, "Sansabet")
    go connectToParser("ws://localhost:7200", &pinnacleConnection, "Pinnacle")
}

func connectToParser(url string, connection **websocket.Conn, name string) {
    for {
        conn, _, err := websocket.DefaultDialer.Dial(url, nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения к %s: %v", name, err)
            time.Sleep(5 * time.Second)
            continue
        }
        *connection = conn
        log.Printf("[DEBUG] Подключение к %s установлено", name)
        go processIncomingMessages(conn, name)
        break
    }
}

func processIncomingMessages(conn *websocket.Conn, name string) {
    log.Printf("[DEBUG] Текущее состояние matchPairs при получении данных: %+v", matchPairs)
    for {
        _, msg, err := conn.ReadMessage()
        log.Printf("[DEBUG] Получены данные из потока %s: %s", name, string(msg))
        if err != nil {
            log.Printf("[ERROR] Ошибка чтения из потока %s: %v", name, err)
            break
        }
        log.Printf("[DEBUG] Потоковые данные от %s: %s", name, string(msg))
        saveMatchData(name, msg)
    }
}

func saveMatchData(name string, msg []byte) {
    log.Printf("[DEBUG] Текущее состояние matchPairs: %+v", matchPairs)

    var parsedMsg struct {
        MatchId string `json:"MatchId"`
    }
    if err := json.Unmarshal(msg, &parsedMsg); err != nil {
        log.Printf("[ERROR] Ошибка парсинга сообщения из %s: %v | Сообщение: %s", name, err, string(msg))
        return
    }

    log.Printf("[DEBUG] Извлечён MatchId из сообщения: %s (длина: %d)", parsedMsg.MatchId, len(parsedMsg.MatchId))

    pairsMutex.Lock()
    defer pairsMutex.Unlock()

    matchFound := false
    for _, pair := range matchPairs {
        log.Printf("[DEBUG] Сравнение с парой: SansabetId=%s, PinnacleId=%s", pair.SansabetId, pair.PinnacleId)

        if len(parsedMsg.MatchId) == 7 { // ID длиной 7 сравнивается с SansabetId
            // я понимаю насколько это костыльно, но я заколебался править это нормально, не выходило
            log.Printf("[DEBUG] Сравнение MatchId=%s с SansabetId=%s", parsedMsg.MatchId, pair.SansabetId)
            if pair.SansabetId == parsedMsg.MatchId {
                matchData["Sansabet"][pair.SansabetId] = string(msg)
                log.Printf("[DEBUG] Совпадение найдено для Sansabet: MatchId=%s", parsedMsg.MatchId)
                matchFound = true
                break
            } else {
                log.Printf("[DEBUG] Не совпало: MatchId=%s != SansabetId=%s", parsedMsg.MatchId, pair.SansabetId)
            }
        } else if len(parsedMsg.MatchId) == 10 { // ID длиной 10 сравнивается с PinnacleId
            log.Printf("[DEBUG] Сравнение MatchId=%s с PinnacleId=%s", parsedMsg.MatchId, pair.PinnacleId)
            if pair.PinnacleId == parsedMsg.MatchId {
                matchData["Pinnacle"][pair.PinnacleId] = string(msg)
                log.Printf("[DEBUG] Совпадение найдено для Pinnacle: MatchId=%s", parsedMsg.MatchId)
                matchFound = true
                break
            } else {
                log.Printf("[DEBUG] Не совпало: MatchId=%s != PinnacleId=%s", parsedMsg.MatchId, pair.PinnacleId)
            }
        } else {
            log.Printf("[DEBUG] Длина MatchId=%d не соответствует известным источникам", len(parsedMsg.MatchId))
        }
    }

    if !matchFound {
        log.Printf("[DEBUG] Нет совпадений для MatchId=%s (длина: %d) в %s | Сообщение: %s", parsedMsg.MatchId, len(parsedMsg.MatchId), name, string(msg))
    }
}



func processPairs() {
    for {
        time.Sleep(1 * time.Second) // Интервал между циклами обработки

        pairsMutex.Lock()
        currentPairs := make([]MatchPair, len(matchPairs))
        copy(currentPairs, matchPairs)
        pairsMutex.Unlock()

        for _, pair := range currentPairs {
            processPair(pair)
        }
    }
}


func processPair(pair MatchPair) {
    pairsMutex.Lock()
    defer pairsMutex.Unlock()

    sansabetData, sansabetExists := matchData["Sansabet"][pair.SansabetId]
    pinnacleData, pinnacleExists := matchData["Pinnacle"][pair.PinnacleId]

    log.Printf("[DEBUG] Проверяем пару: %s | SansabetId=%s | PinnacleId=%s", pair.MatchName, pair.SansabetId, pair.PinnacleId)
    log.Printf("[DEBUG] Данные в matchData для Sansabet: %v", matchData["Sansabet"])
    log.Printf("[DEBUG] Данные в matchData для Pinnacle: %v", matchData["Pinnacle"])

    if !sansabetExists {
        log.Printf("[DEBUG] Нет данных для SansabetId=%s", pair.SansabetId)
    }
    if !pinnacleExists {
        log.Printf("[DEBUG] Нет данных для PinnacleId=%s", pair.PinnacleId)
    }

    if sansabetExists && pinnacleExists {
        log.Printf("[DEBUG] Найдены данные для пары: %s | SansabetData=%s | PinnacleData=%s", pair.MatchName, sansabetData, pinnacleData)
        analyzeAndSend(pair.MatchName, sansabetData, pinnacleData)
    }
}




func analyzeAndSend(matchName, sansabetData, pinnacleData string) {
    commonOutcomes := findCommonOutcomes(sansabetData, pinnacleData)
    log.Printf("[DEBUG] Общие исходы для пары %s: %+v", matchName, commonOutcomes)
    if len(commonOutcomes) == 0 {
        log.Printf("[DEBUG] Нет общих исходов для матча %s", matchName)
        return
    }

    filtered := calculateAndFilterCommonOutcomes(matchName, commonOutcomes)
    if len(filtered) == 0 {
        log.Printf("[DEBUG] Нет подходящих исходов для матча %s", matchName)
        return
    }

    for _, result := range filtered {
        forwardToFrontend([]byte(result))
    }
}

func forwardToFrontend(data []byte) {
    log.Printf("[DEBUG] Отправка данных клиентам: %s", string(data))
    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[ERROR] Ошибка отправки данных клиенту: %v", err)
            client.Close()
            delete(frontendClients, client)
        }
    }
}

func startFrontendServer() {
    http.HandleFunc("/output", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения клиента: %v", err)
            return
        }

        frontendMutex.Lock()
        frontendClients[conn] = true
        frontendMutex.Unlock()

        defer func() {
            frontendMutex.Lock()
            delete(frontendClients, conn)
            frontendMutex.Unlock()
        }()

        for {
            if _, _, err := conn.NextReader(); err != nil {
                break
            }
        }
    })

    log.Fatal(http.ListenAndServe(":7300", nil))
}

func main() {
    startAnalyzer()
    select {}
}
