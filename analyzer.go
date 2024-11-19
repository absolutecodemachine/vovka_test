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

var matchingConnection *websocket.Conn
var matchingMutex sync.Mutex

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
func calculateAndFilterCommonOutcomes(matchName string, commonOutcomes map[string][2]float64) []map[string]interface{} {
    filtered := []map[string]interface{}{}

    for outcome, values := range commonOutcomes {
        roi := calculateROI(values[0], values[1])
        if roi > -30 {
            filtered = append(filtered, map[string]interface{}{
                "Outcome":  outcome,
                "ROI":      roi,
                "Sansabet": values[0],
                "Pinnacle": values[1],
            })
        }
    }

    sort.Slice(filtered, func(i, j int) bool {
        return filtered[i]["ROI"].(float64) > filtered[j]["ROI"].(float64)
    })

    return filtered
}

var matchPairs []MatchPair
var pairsMutex sync.Mutex

var frontendClients = make(map[*websocket.Conn]bool)
var frontendMutex = sync.Mutex{}
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// Мапы для хранения подключений от парсеров
var sansabetConnections = make(map[*websocket.Conn]bool)
var pinnacleConnections = make(map[*websocket.Conn]bool)

// Запуск анализатора
func startAnalyzer() {
    log.Println("[DEBUG] Анализатор запущен")
    go startParserServer(7100, "Sansabet")
    go startParserServer(7200, "Pinnacle")
    go startFrontendServer()
    go startMatchingServer()

    go processPairs()
}

// Сервер для приема данных от парсеров
func startParserServer(port int, sourceName string) {
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения от %s: %v", sourceName, err)
            return
        }
        defer conn.Close()

        log.Printf("[DEBUG] Новое подключение от парсера %s", sourceName)

        if sourceName == "Sansabet" {
            sansabetConnections[conn] = true
        } else if sourceName == "Pinnacle" {
            pinnacleConnections[conn] = true
        }

        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Printf("[ERROR] Ошибка чтения сообщения от %s: %v", sourceName, err)
                break
            }

            // Обрабатываем сообщение, указывая источник
            saveMatchData(sourceName, msg)
        }

        // Удаляем соединение после закрытия
        if sourceName == "Sansabet" {
            delete(sansabetConnections, conn)
        } else if sourceName == "Pinnacle" {
            delete(pinnacleConnections, conn)
        }
    })

    log.Printf("[DEBUG] Сервер для парсера %s запущен на порту %d", sourceName, port)
    err := http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
    if err != nil {
        log.Printf("[ERROR] Ошибка запуска сервера для парсера %s: %v", sourceName, err)
    }
}

// Хендлер для подключения мэтчинга
func startMatchingServer() {
    mux := http.NewServeMux()
    mux.HandleFunc("/matching", matchingHandler)
    log.Println("[DEBUG] Сервер для мэтчинга запущен на порту 7400")
    err := http.ListenAndServe(":7400", mux)
    if err != nil {
        log.Printf("[ERROR] Ошибка запуска сервера для мэтчинга: %v", err)
    }
}

func matchingHandler(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("[ERROR] Ошибка подключения мэтчинга: %v", err)
        return
    }

    log.Printf("[DEBUG] Новое подключение мэтчинга")

    matchingMutex.Lock()
    matchingConnection = conn
    matchingMutex.Unlock()

    defer func() {
        matchingMutex.Lock()
        if matchingConnection == conn {
            matchingConnection = nil
        }
        matchingMutex.Unlock()
        conn.Close()
    }()

    for {
        // Keep the connection open; read messages if needed
        if _, _, err := conn.ReadMessage(); err != nil {
            log.Printf("[ERROR] Ошибка чтения от мэтчинга: %v", err)
            break
        }
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

        if name == "Sansabet" && pair.SansabetId == parsedMsg.MatchId {
            matchData["Sansabet"][pair.SansabetId] = string(msg)
            log.Printf("[DEBUG] Совпадение найдено для Sansabet: MatchId=%s", parsedMsg.MatchId)
            matchFound = true
            break
        } else if name == "Pinnacle" && pair.PinnacleId == parsedMsg.MatchId {
            matchData["Pinnacle"][pair.PinnacleId] = string(msg)
            log.Printf("[DEBUG] Совпадение найдено для Pinnacle: MatchId=%s", parsedMsg.MatchId)
            matchFound = true
            break
        }
    }

    if !matchFound {
        log.Printf("[DEBUG] Нет совпадений для MatchId=%s в %s | Сообщение: %s", parsedMsg.MatchId, name, string(msg))
    }
    matchingMutex.Lock()
    defer matchingMutex.Unlock()
    if matchingConnection != nil {
        err := matchingConnection.WriteMessage(websocket.TextMessage, msg)
        if err != nil {
            log.Printf("[ERROR] Ошибка отправки данных в мэтчинг: %v", err)
            matchingConnection = nil // Reset connection
        }
    }
}

func processPairs() {
    for {
        time.Sleep(500 * time.Millisecond) // Пауза 0.5 секунды

        pairsMutex.Lock()
        currentPairs := make([]MatchPair, len(matchPairs))
        copy(currentPairs, matchPairs)
        pairsMutex.Unlock()

        results := groupResultsByMatch(currentPairs)
        if len(results) > 0 {
            forwardToFrontendBatch(results)
        }
    }
}

func groupResultsByMatch(pairs []MatchPair) []map[string]interface{} {
    results := []map[string]interface{}{}
    for _, pair := range pairs {
        result := processPairAndGetResult(pair)
        if result != nil {
            results = append(results, result)
        }
    }
    return results
}

func processPairAndGetResult(pair MatchPair) map[string]interface{} {
    sansabetData, sansabetExists := matchData["Sansabet"][pair.SansabetId]
    pinnacleData, pinnacleExists := matchData["Pinnacle"][pair.PinnacleId]

    if !sansabetExists || !pinnacleExists {
        log.Printf("[DEBUG] Данные для пары отсутствуют: SansabetId=%s, PinnacleId=%s", pair.SansabetId, pair.PinnacleId)
        return nil
    }

    log.Printf("[DEBUG] Анализируем пару: %s | SansabetId=%s | PinnacleId=%s", pair.MatchName, pair.SansabetId, pair.PinnacleId)

    commonOutcomes := findCommonOutcomes(sansabetData, pinnacleData)
    if len(commonOutcomes) == 0 {
        log.Printf("[DEBUG] Нет общих исходов для пары: %s", pair.MatchName)
        return nil
    }

    filtered := calculateAndFilterCommonOutcomes(pair.MatchName, commonOutcomes)
    if len(filtered) == 0 {
        log.Printf("[DEBUG] Нет подходящих исходов для пары: %s", pair.MatchName)
        return nil
    }

    // Формируем результат в формате JSON-совместимой структуры
    result := map[string]interface{}{
        "MatchName":  pair.MatchName,
        "SansabetId": pair.SansabetId,
        "PinnacleId": pair.PinnacleId,
        "LeagueName": "", // Будет извлечено из sansabetData и pinnacleData
        "Country":    "", // Если есть
        "Outcomes":   filtered,
    }

    // Добавляем данные лиги и страны, если присутствуют
    var sansabetParsed, pinnacleParsed map[string]interface{}
    json.Unmarshal([]byte(sansabetData), &sansabetParsed)
    json.Unmarshal([]byte(pinnacleData), &pinnacleParsed)

    if leagueName, ok := sansabetParsed["LeagueName"].(string); ok && leagueName != "" {
        result["LeagueName"] = leagueName
    } else if leagueName, ok := pinnacleParsed["LeagueName"].(string); ok {
        result["LeagueName"] = leagueName
    }

    if country, ok := sansabetParsed["Country"].(string); ok {
        result["Country"] = country
    }

    // Удаляем обработанные данные
    delete(matchData["Sansabet"], pair.SansabetId)
    delete(matchData["Pinnacle"], pair.PinnacleId)

    return result
}

func forwardToFrontendBatch(results []map[string]interface{}) {
    log.Printf("[DEBUG] Отправка %d матчей клиентам", len(results))

    // Кодируем результаты в JSON
    data, err := json.Marshal(results)
    if err != nil {
        log.Printf("[ERROR] Ошибка кодирования JSON: %v", err)
        return
    }

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
    mux := http.NewServeMux()
    mux.HandleFunc("/output", func(w http.ResponseWriter, r *http.Request) {
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

    log.Println("[DEBUG] Сервер для фронтенда запущен на порту 7300")
    err := http.ListenAndServe(":7300", mux)
    if err != nil {
        log.Printf("[ERROR] Ошибка запуска сервера для фронтенда: %v", err)
    }
}

func main() {
    startAnalyzer()
    select {} // Блокируем основную горутину, чтобы программа не завершалась
}
