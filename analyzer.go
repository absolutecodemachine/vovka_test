package main

import (
    "crypto/sha1"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "sort"
    "strings"
    "sync"
    "time"

    _ "github.com/go-sql-driver/mysql"
    "github.com/gorilla/websocket"
)

var db *sql.DB

type ParsedMessage struct {
    MatchId   string
    MatchName string
    Home      string
    Away      string
}

// Инициализация подключения к базе
func initDB() {
    var err error
    dsn := "matchingTeams:local_password@tcp(localhost:3306)/matchingTeams"
    db, err = sql.Open("mysql", dsn)
    if err != nil {
        log.Fatalf("[ERROR] Ошибка подключения к базе данных: %v", err)
    }

    // Проверяем соединение
    if err := db.Ping(); err != nil {
        log.Fatalf("[ERROR] Не удалось подключиться к базе: %v", err)
    }

    log.Println("[DEBUG] Подключение к базе данных успешно установлено")
}

// Вставка команды
func insertTeam(source, teamName string) {
    query := fmt.Sprintf("INSERT INTO %sTeams(teamName) VALUES (?)", strings.ToLower(source))
    _, err := db.Exec(query, teamName)
    if err != nil {
        log.Printf("[ERROR] Ошибка вставки команды %s в %s: %v", teamName, source, err)
        return
    }
    log.Printf("[DEBUG] Команда %s успешно добавлена в %s", teamName, source)
}

// Проверка команды
func teamExists(source, teamName string) bool {
    query := fmt.Sprintf("SELECT id FROM %sTeams WHERE teamName = ? LIMIT 1", strings.ToLower(source))
    row := db.QueryRow(query, teamName)

    var id int
    if err := row.Scan(&id); err != nil {
        if err == sql.ErrNoRows {
            log.Printf("[DEBUG] Команда %s отсутствует в %s", teamName, source)
            return false
        }
        log.Printf("[ERROR] Ошибка проверки команды %s в %s: %v", teamName, source, err)
        return false
    }
    log.Printf("[DEBUG] Команда %s найдена в %s", teamName, source)
    return true
}

// Генерация ключа матча
func generateMatchKey(home, away string) string {
    h1 := sha1.Sum([]byte(home))
    h2 := sha1.Sum([]byte(away))
    return hex.EncodeToString(h1[:]) + hex.EncodeToString(h2[:])
}

var matchData = map[string]map[string]string{
    "Sansabet": make(map[string]string),
    "Pinnacle": make(map[string]string),
}

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
    Home       string
    Away       string
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

var matchingConnection *websocket.Conn
var matchingMutex sync.Mutex

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

// Функция для разделения названия матча на домашнюю и гостевую команды
func splitMatchName(matchName, source string) []string {
    log.Printf("[DEBUG] Разделяем MatchName: %s для источника %s", matchName, source)

    var separator string
    if source == "Sansabet" {
        separator = " : "
    } else if source == "Pinnacle" {
        separator = " vs "
    } else {
        separator = " vs "
    }

    teams := strings.Split(matchName, separator)
    log.Printf("[DEBUG] Разделённые команды: %v", teams)
    return teams
}

func updateMatchPairs(source string, parsedMsg *ParsedMessage) {
    var updated bool

    pairsMutex.Lock()
    defer pairsMutex.Unlock()

    for i, pair := range matchPairs {
        if pair.Home == parsedMsg.Home && pair.Away == parsedMsg.Away {
            if source == "Sansabet" && pair.SansabetId == "" {
                matchPairs[i].SansabetId = parsedMsg.MatchId
                updated = true
            } else if source == "Pinnacle" && pair.PinnacleId == "" {
                matchPairs[i].PinnacleId = parsedMsg.MatchId
                updated = true
            }

            if matchPairs[i].SansabetId != "" && matchPairs[i].PinnacleId != "" {
                log.Printf("[DEBUG] Полная пара найдена: %v", matchPairs[i])
            }
            break
        }
    }

    if !updated {
        // Добавляем новую пару, если ничего не обновлено
        newPair := MatchPair{
            Home:      parsedMsg.Home,
            Away:      parsedMsg.Away,
            MatchName: parsedMsg.Home + " vs " + parsedMsg.Away,
        }
        if source == "Sansabet" {
            newPair.SansabetId = parsedMsg.MatchId
        } else if source == "Pinnacle" {
            newPair.PinnacleId = parsedMsg.MatchId
        }
        matchPairs = append(matchPairs, newPair)

        log.Printf("[DEBUG] Новая пара добавлена в matchPairs: %v", newPair)
    }
}

func saveMatchData(name string, msg []byte) {
    log.Printf("[DEBUG] Начало обработки сообщения из %s", name)

    // Парсинг сообщения
    parsedMsg, err := parseMessage(name, msg)
    if err != nil {
        log.Printf("[ERROR] %v", err)
        return
    }

    // Проверка и добавление команд в базу
    ensureTeamsExist(name, parsedMsg.Home, parsedMsg.Away)

    // Связывание команд
    homeLinked, awayLinked := linkTeamsForMatch(name, parsedMsg.Home, parsedMsg.Away)

    // Обновляем matchPairs только если обе команды связаны
    if homeLinked && awayLinked {
        updateMatchPairs(name, parsedMsg)
    }

    // Генерируем ключ и сохраняем данные в matchData
    key := generateMatchKey(parsedMsg.Home, parsedMsg.Away)
    matchData[name][key] = string(msg)

    log.Printf("[DEBUG] Данные матча сохранены в matchData[%s][%s]", name, key)
    log.Printf("[DEBUG] Завершение обработки сообщения из %s", name)
}

func parseMessage(name string, msg []byte) (*ParsedMessage, error) {
    log.Printf("[DEBUG] Получено сообщение из %s: %s", name, string(msg))

    var parsedMsg ParsedMessage

    if err := json.Unmarshal(msg, &parsedMsg); err != nil {
        return nil, fmt.Errorf("Ошибка парсинга JSON из %s: %v | Сообщение: %s", name, err, string(msg))
    }

    log.Printf("[DEBUG] Распарсенные данные: MatchId=%s, MatchName=%s", parsedMsg.MatchId, parsedMsg.MatchName)

    teams := splitMatchName(parsedMsg.MatchName, name)
    if len(teams) != 2 {
        return nil, fmt.Errorf("Не удалось разделить MatchName на команды: %s", parsedMsg.MatchName)
    }

    parsedMsg.Home = teams[0]
    parsedMsg.Away = teams[1]
    log.Printf("[DEBUG] Извлечённые команды: Home=%s, Away=%s", parsedMsg.Home, parsedMsg.Away)

    if parsedMsg.Home == "" || parsedMsg.Away == "" {
        return nil, fmt.Errorf("Пустые данные команды: Home=%s, Away=%s", parsedMsg.Home, parsedMsg.Away)
    }

    return &parsedMsg, nil
}

func ensureTeamsExist(source, home, away string) {
    log.Printf("[DEBUG] Проверяем наличие команды %s в %s", home, source)
    if !teamExists(source, home) {
        log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её", home, source)
        insertTeam(source, home)
    }

    log.Printf("[DEBUG] Проверяем наличие команды %s в %s", away, source)
    if !teamExists(source, away) {
        log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её", away, source)
        insertTeam(source, away)
    }
}

// Проверка и связывание команд для матча
func linkTeamsForMatch(source, home, away string) (bool, bool) {
    log.Printf("[DEBUG] Пытаемся найти связи для матча из %s: Home=%s, Away=%s", source, home, away)

    // Проверяем наличие соответствий для обеих команд
    homeLinked := linkTeam(home, source)
    awayLinked := linkTeam(away, source)

    if homeLinked && awayLinked {
        log.Printf("[DEBUG] Оба соответствия найдены: Home=%s, Away=%s", home, away)
    } else {
        log.Printf("[DEBUG] Не удалось найти соответствия: HomeLinked=%t, AwayLinked=%t", homeLinked, awayLinked)
    }

    return homeLinked, awayLinked
}

// Проверяем связь для одной команды
func linkTeam(teamName, source string) bool {
    log.Printf("[DEBUG] Ищем связь для команды %s из источника %s", teamName, source)

    var query string
    var linkedId int

    if source == "Sansabet" {
        // Проверяем pinnacleId у команды из sansabetTeams
        query = `SELECT pinnacleId 
                 FROM sansabetTeams 
                 WHERE teamName = ? AND pinnacleId > 0 LIMIT 1`
    } else if source == "Pinnacle" {
        // Проверяем, есть ли команда в sansabetTeams, связанная с pinnacleTeams
        query = `SELECT id 
                 FROM sansabetTeams 
                 WHERE pinnacleId = (SELECT id FROM pinnacleTeams WHERE teamName = ?) LIMIT 1`
    } else {
        log.Printf("[ERROR] Неизвестный источник: %s", source)
        return false
    }

    // Выполняем запрос
    err := db.QueryRow(query, teamName).Scan(&linkedId)
    if err != nil {
        if err == sql.ErrNoRows {
            log.Printf("[DEBUG] Связь не найдена для команды %s в источнике %s", teamName, source)
        } else {
            log.Printf("[ERROR] Ошибка запроса для команды %s в источнике %s: %v", teamName, source, err)
        }
        return false
    }

    log.Printf("[DEBUG] Связь найдена для команды %s в источнике %s: LinkedId=%d", teamName, source, linkedId)
    return true
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
    key := generateMatchKey(pair.Home, pair.Away)
    sansabetData, sansabetExists := matchData["Sansabet"][key]
    pinnacleData, pinnacleExists := matchData["Pinnacle"][key]

    if !sansabetExists || !pinnacleExists {
        log.Printf("[DEBUG] Данные для пары отсутствуют: SansabetKey=%s, PinnacleKey=%s", key, key)
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

    // Формируем результат
    result := map[string]interface{}{
        "MatchName":  pair.MatchName,
        "SansabetId": pair.SansabetId,
        "PinnacleId": pair.PinnacleId,
        "LeagueName": "",
        "Country":    "",
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
    delete(matchData["Sansabet"], key)
    delete(matchData["Pinnacle"], key)

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
            conn.Close()
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
    initDB() // Обязательно инициализируем базу
    startAnalyzer()
    select {} // Блокируем основную горутину
}
