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

// Подключение к базе данных
var db *sql.DB

// Мьютексы и переменные для синхронизации
var (
    lastSentResults []map[string]interface{}
    lastSentMutex   sync.Mutex
    pairsMutex      sync.Mutex
    frontendMutex   sync.Mutex
)

// Структуры для хранения данных
type ParsedMessage struct {
    MatchId   string
    MatchName string
    Home      string
    Away      string
}

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
    Home       string
    Away       string
}

var (
    matchData = map[string]map[string]string{
        "Sansabet": make(map[string]string),
        "Pinnacle": make(map[string]string),
    }

    matchPairs []MatchPair
    matchKeys  = make(map[string]bool) // Уникальные ключи матчей

    // Клиенты фронтенда
    frontendClients = make(map[*websocket.Conn]bool)

    // Соединения парсеров
    sansabetConnections  = make(map[*websocket.Conn]bool)
    pinnacleConnections  = make(map[*websocket.Conn]bool)
    upgrader             = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
    extraPercents        = []struct {
        Min, Max, ExtraPercent float64
    }{
        {2.29, 2.75, 1.03},
        {2.75, 3.2, 1.04},
        {3.2, 3.7, 1.05},
    }
)

const MARGIN = 1.08

// Инициализация подключения к базе данных
func initDB() {
    var err error
    dsn := "matchingTeams:local_password@tcp(localhost:3306)/matchingTeams"
    db, err = sql.Open("mysql", dsn)
    if err != nil {
        log.Fatalf("[ERROR] Ошибка подключения к базе данных: %v", err)
    }
    if err := db.Ping(); err != nil {
        log.Fatalf("[ERROR] Не удалось подключиться к базе: %v", err)
    }
    log.Println("[DEBUG] Подключение к базе данных успешно установлено")
}

// Запуск анализатора
func startAnalyzer() {
    log.Println("[DEBUG] Анализатор запущен")
    go startParserServer(7100, "Sansabet")
    go startParserServer(7200, "Pinnacle")
    go startFrontendServer()
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
            saveMatchData(sourceName, msg)
        }

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

// Сервер для фронтенда
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

// Обработка полученных данных от парсеров
func saveMatchData(name string, msg []byte) {
    log.Printf("[DEBUG] Начало обработки сообщения из %s", name)

    parsedMsg, err := parseMessage(name, msg)
    if err != nil {
        log.Printf("[ERROR] %v", err)
        return
    }

    ensureTeamsExist(name, parsedMsg.Home, parsedMsg.Away)
    homeLinked, awayLinked := linkTeamsForMatch(name, parsedMsg.Home, parsedMsg.Away)

    if homeLinked && awayLinked {
        updateMatchPairs(name, parsedMsg)
    }

    key := generateMatchKey(parsedMsg.Home, parsedMsg.Away)
    matchData[name][key] = string(msg)

    log.Printf("[DEBUG] Данные матча сохранены в matchData[%s][%s]", name, key)
    log.Printf("[DEBUG] Завершение обработки сообщения из %s", name)
}

// Парсинг сообщения
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

// Разделение названия матча на домашнюю и гостевую команды
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

// Проверка наличия команд в базе данных
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

// Проверка наличия команды в базе данных
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

// Вставка команды в базу данных
func insertTeam(source, teamName string) {
    query := fmt.Sprintf("INSERT INTO %sTeams(teamName) VALUES (?)", strings.ToLower(source))
    _, err := db.Exec(query, teamName)
    if err != nil {
        log.Printf("[ERROR] Ошибка вставки команды %s в %s: %v", teamName, source, err)
        return
    }
    log.Printf("[DEBUG] Команда %s успешно добавлена в %s", teamName, source)
}

// Проверка и связывание команд для матча
func linkTeamsForMatch(source, home, away string) (bool, bool) {
    log.Printf("[DEBUG] Пытаемся найти связи для матча из %s: Home=%s, Away=%s", source, home, away)

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
        query = `SELECT pinnacleId FROM sansabetTeams WHERE teamName = ? AND pinnacleId > 0 LIMIT 1`
    } else if source == "Pinnacle" {
        query = `SELECT id FROM sansabetTeams WHERE pinnacleId = (SELECT id FROM pinnacleTeams WHERE teamName = ?) LIMIT 1`
    } else {
        log.Printf("[ERROR] Неизвестный источник: %s", source)
        return false
    }

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

// Обновление matchPairs
func updateMatchPairs(source string, parsedMsg *ParsedMessage) {
    key := generateMatchKey(parsedMsg.Home, parsedMsg.Away)

    pairsMutex.Lock()
    defer pairsMutex.Unlock()

    if _, exists := matchKeys[key]; exists {
        log.Printf("[DEBUG] Матч уже существует: %s vs %s", parsedMsg.Home, parsedMsg.Away)
        return
    }

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
    matchKeys[key] = true

    log.Printf("[DEBUG] Новая пара добавлена в matchPairs: %v", newPair)
}

// Генерация ключа матча
func generateMatchKey(home, away string) string {
    h1 := sha1.Sum([]byte(home))
    h2 := sha1.Sum([]byte(away))
    return hex.EncodeToString(h1[:]) + hex.EncodeToString(h2[:])
}

// Обработка пар матчей
func processPairs() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        pairsMutex.Lock()
        currentPairs := make([]MatchPair, len(matchPairs))
        copy(currentPairs, matchPairs)
        pairsMutex.Unlock()

        results := groupResultsByMatch(currentPairs)

        if len(results) > 0 {
            lastSentMutex.Lock()
            lastSentResults = results
            lastSentMutex.Unlock()

            log.Printf("[DEBUG] Новые данные для отправки: %d результатов", len(results))
        } else {
            log.Printf("[DEBUG] Новых данных нет, отправляем последний сохраненный результат")
            lastSentMutex.Lock()
            results = lastSentResults
            lastSentMutex.Unlock()
        }

        forwardToFrontendBatch(results)

        if len(results) > 0 {
            pairsMutex.Lock()
            for _, pair := range currentPairs {
                key := generateMatchKey(pair.Home, pair.Away)
                delete(matchData["Sansabet"], key)
                delete(matchData["Pinnacle"], key)
            }
            log.Printf("[DEBUG] Обработанные данные удалены")
            pairsMutex.Unlock()
        }
    }
}

// Группировка результатов по матчам
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

// Обработка одной пары и получение результата
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

    result := map[string]interface{}{
        "MatchName":  pair.MatchName,
        "SansabetId": pair.SansabetId,
        "PinnacleId": pair.PinnacleId,
        "LeagueName": "",
        "Country":    "",
        "Outcomes":   filtered,
    }

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

    return result
}

// Поиск общих исходов
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

// Расчет ROI
func calculateROI(sansaOdd, pinnacleOdd float64) float64 {
    extraPercent := getExtraPercent(pinnacleOdd)
    roi := (sansaOdd/(pinnacleOdd*MARGIN*extraPercent) - 1 - 0.03) * 100 * 0.67
    return roi
}

// Получение дополнительного процента
func getExtraPercent(pinnacleOdd float64) float64 {
    for _, ep := range extraPercents {
        if pinnacleOdd >= ep.Min && pinnacleOdd < ep.Max {
            return ep.ExtraPercent
        }
    }
    return 1.0
}

// Расчет и фильтрация общих исходов
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

// Отправка данных клиентам фронтенда
func forwardToFrontendBatch(results []map[string]interface{}) {
    log.Printf("[DEBUG] Подготовка данных для отправки клиентам")

    data, err := json.Marshal(results)
    if err != nil {
        log.Printf("[ERROR] Ошибка кодирования JSON: %v", err)
        return
    }

    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    if len(frontendClients) == 0 {
        log.Printf("[DEBUG] Нет подключенных клиентов, данные не отправлены")
        return
    }

    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[ERROR] Ошибка отправки данных клиенту: %v", err)
            client.Close()
            delete(frontendClients, client)
        } else {
            log.Printf("[DEBUG] Данные отправлены клиенту, количество матчей: %d", len(results))
        }
    }
}

func main() {
    initDB()
    startAnalyzer()
    select {}
}
