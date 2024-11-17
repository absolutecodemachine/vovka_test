package main

import (
    "encoding/json"
    "log"
    "net/http"
    "sync"
    "time"
    "fmt"
    "github.com/gorilla/websocket"
)

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
}

type OddsData struct {
    Outcomes map[string]float64 `json:"outcomes"` // Пример: {"TotalOver2.5": 1.9, "TotalUnder2.5": 1.8}
}

// Функция для нахождения пересечений
func findCommonOutcomes(sansabetData, pinnacleData string) map[string][2]float64 {
    log.Printf("[Analyzer] Входные данные Sansabet: %s", sansabetData)
    log.Printf("[Analyzer] Входные данные Pinnacle: %s", pinnacleData)

    var sansabetOdds, pinnacleOdds struct {
        Win1x2   map[string]float64 `json:"Win1x2"`
        Totals   map[string]struct {
            WinMore float64 `json:"WinMore"`
            WinLess float64 `json:"WinLess"`
        } `json:"Totals"`
        Handicap map[string]struct {
            Win1 float64 `json:"Win1"`
            Win2 float64 `json:"Win2"`
        } `json:"Handicap"`
    }

    common := make(map[string][2]float64)

    // Парсим данные
    if err := json.Unmarshal([]byte(sansabetData), &sansabetOdds); err != nil {
        log.Printf("[Analyzer] Ошибка парсинга данных Sansabet: %v", err)
        return common
    }
    log.Printf("[Analyzer] Распарсенные данные Sansabet: %+v", sansabetOdds)

    if err := json.Unmarshal([]byte(pinnacleData), &pinnacleOdds); err != nil {
        log.Printf("[Analyzer] Ошибка парсинга данных Pinnacle: %v", err)
        return common
    }
    log.Printf("[Analyzer] Распарсенные данные Pinnacle: %+v", pinnacleOdds)

    // Сравниваем Win1x2
    for key, sansabetValue := range sansabetOdds.Win1x2 {
        if pinnacleValue, exists := pinnacleOdds.Win1x2[key]; exists {
            common[key] = [2]float64{sansabetValue, pinnacleValue}
            log.Printf("[Analyzer] Общий исход (Win1x2): %s, Sansabet=%f, Pinnacle=%f", key, sansabetValue, pinnacleValue)
        }
    }

    // Сравниваем Totals
    for key, sansabetTotal := range sansabetOdds.Totals {
        if pinnacleTotal, exists := pinnacleOdds.Totals[key]; exists {
            common["Total "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
            log.Printf("[Analyzer] Общий исход (Totals): %s, Sansabet=%f, Pinnacle=%f", key, sansabetTotal.WinMore, pinnacleTotal.WinMore)
        }
    }

    // Сравниваем Handicap
    for key, sansabetHandicap := range sansabetOdds.Handicap {
        if pinnacleHandicap, exists := pinnacleOdds.Handicap[key]; exists {
            common["Handicap "+key] = [2]float64{sansabetHandicap.Win1, pinnacleHandicap.Win1}
            log.Printf("[Analyzer] Общий исход (Handicap): %s, Sansabet=%f, Pinnacle=%f", key, sansabetHandicap.Win1, pinnacleHandicap.Win1)
        }
    }

    log.Printf("[Analyzer] Общие исходы: %+v", common)
    return common
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
    log.Println("[Analyzer] Анализатор запущен")
    go connectParsers()
    go processPairs()
    go startFrontendServer()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения: %v", err)
            return
        }
        defer conn.Close()

        for {
            _, msg, err := conn.ReadMessage()
            //log.Printf("[Analyzer] Полученные данные от мэтчинга: %s", string(msg))
            if err != nil {
                log.Printf("[Analyzer] Ошибка чтения данных: %v", err)
                break
            }

            var pairs []MatchPair
            if err := json.Unmarshal(msg, &pairs); err != nil {
                log.Printf("[Analyzer] Ошибка парсинга данных: %v", err)
                continue
            }

            pairsMutex.Lock()
            matchPairs = pairs
            pairsMutex.Unlock()
            //log.Printf("[Analyzer] Обновлён список пар: %v", pairs)
        }
    })

    log.Fatal(http.ListenAndServe(":7400", nil))
}

// Подключение к парсерам
func connectParsers() {
    go connectToParser("ws://localhost:7100", &sansabetConnection, "Sansabet")
    go connectToParser("ws://localhost:7200", &pinnacleConnection, "Pinnacle")
}

func connectToParser(url string, connection **websocket.Conn, name string) {
    for {
        conn, _, err := websocket.DefaultDialer.Dial(url, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения к %s: %v", name, err)
            time.Sleep(5 * time.Second)
            continue
        }
        *connection = conn
        log.Printf("[Analyzer] Подключение к %s установлено", name)
        break
    }
}

// Обработка пар
func processPairs() {
    for {
        time.Sleep(1 * time.Second)

        pairsMutex.Lock()
        currentPairs := make([]MatchPair, len(matchPairs))
        copy(currentPairs, matchPairs)
        pairsMutex.Unlock()
        //log.Printf("[Analyzer] Проверка текущих пар: %v", currentPairs)

        for _, pair := range currentPairs {
            processPair(pair)
        }
    }
}

// Обработка одной пары
func processPair(pair MatchPair) {
    sansabetData := fetchOdds(sansabetConnection, pair.SansabetId)
    pinnacleData := fetchOdds(pinnacleConnection, pair.PinnacleId)
    //log.Printf("[Analyzer] Данные для анализа: Sansabet=%s, Pinnacle=%s", sansabetData, pinnacleData)
    if sansabetData != "" && pinnacleData != "" {
        analyzeAndSend(pair.MatchName, sansabetData, pinnacleData)
    }
}

// Запрос данных
func fetchOdds(conn *websocket.Conn, matchId string) string {
    if conn == nil {
        log.Printf("[Analyzer] fetchOdds: conn == nil для MatchId=%s", matchId)
        return ""
    }

    log.Printf("[Analyzer] fetchOdds: Отправка ID: %s", matchId)
    if err := conn.WriteMessage(websocket.TextMessage, []byte(matchId)); err != nil {
        log.Printf("[Analyzer] fetchOdds: Ошибка отправки ID: %v", err)
        return ""
    }

    _, msg, err := conn.ReadMessage()
    if err != nil {
        log.Printf("[Analyzer] fetchOdds: Ошибка чтения данных: %v", err)
        return ""
    }

    log.Printf("[Analyzer] fetchOdds: Исходные данные от парсера (MatchId=%s): %s", matchId, string(msg))
    return string(msg)
}


// Анализ данных и отправка на сайт
func analyzeAndSend(matchName, sansabetData, pinnacleData string) {
    log.Printf("[Analyzer] Начало анализа для матча: %s", matchName)
    commonOutcomes := findCommonOutcomes(sansabetData, pinnacleData)
    if len(commonOutcomes) == 0 {
        log.Printf("[Analyzer] Общих исходов не найдено для матча: %s", matchName)
        return
    }

    for outcome, values := range commonOutcomes {
        message := matchName + " | Исход: " + outcome +
            " | Sansabet: " + formatFloat(values[0]) +
            " | Pinnacle: " + formatFloat(values[1])
        log.Printf("[Analyzer] Отправка данных на сайт: %s", message)
        forwardToFrontend([]byte(message))
    }
}

// Вспомогательная функция для форматирования float
func formatFloat(value float64) string {
    return fmt.Sprintf("%.2f", value)
}


// Отправка на сайт
func forwardToFrontend(data []byte) {
    log.Printf("[Analyzer] Данные для отправки клиентам: %s", string(data))

    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[Analyzer] Ошибка отправки данных клиенту: %v", err)
            client.Close()
            delete(frontendClients, client)
        }
    }
}

// Запуск фронтенд-сервера
func startFrontendServer() {
    http.HandleFunc("/output", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения клиента: %v", err)
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

// Главная функция
func main() {
    startAnalyzer()
    select {}
}