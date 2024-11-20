package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "sort"
    "sync"
    "time"
    "crypto/sha1"
    "encoding/hex"
    "database/sql"
    "strings"
    _ "github.com/go-sql-driver/mysql"
    "github.com/gorilla/websocket"
)

var db *sql.DB

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

// Полный матчинг
func getTeamMatching() []map[string]string {
    query := `
        SELECT 
            sansabetTeams.id as sansabetId, 
            pinnacleTeams.id as pinnacleId, 
            sansabetTeams.teamName as sansabetName, 
            pinnacleTeams.teamName as pinnacleName
        FROM 
            sansabetTeams 
        LEFT JOIN 
            pinnacleTeams 
        ON 
            (sansabetTeams.pinnacleId = pinnacleTeams.id)
        LIMIT 1
    `

    rows, err := db.Query(query)
    if err != nil {
        log.Printf("[ERROR] Ошибка получения матчей: %v", err)
        return nil
    }
    defer rows.Close()

    var results []map[string]string
    for rows.Next() {
        var sansabetId, pinnacleId, sansabetName, pinnacleName string
        if err := rows.Scan(&sansabetId, &pinnacleId, &sansabetName, &pinnacleName); err != nil {
            log.Printf("[ERROR] Ошибка чтения строки: %v", err)
            continue
        }
        results = append(results, map[string]string{
            "sansabetId":   sansabetId,
            "pinnacleId":   pinnacleId,
            "sansabetName": sansabetName,
            "pinnacleName": pinnacleName,
        })
    }
    return results
}

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

func splitMatchName(matchName, source string) []string {
    log.Printf("[DEBUG] Разделяем MatchName: %s для источника %s", matchName, source)

    var separator string
    if source == "Sansabet" {
        separator = " : "
    } else if source == "Pinnacle" {
        separator = " vs "
    } else {
        log.Printf("[ERROR] Неизвестный источник: %s", source)
        return nil
    }

    teams := strings.Split(matchName, separator)
    log.Printf("[DEBUG] Разделённые команды: %v", teams)
    return teams
}


func saveMatchData(name string, msg []byte) {
    log.Printf("[DEBUG] Начало обработки сообщения из %s", name)

    // Логируем полученное сообщение
    log.Printf("[DEBUG] Получено сообщение из %s: %s", name, string(msg))

    // Структура для парсинга JSON
    var parsedMsg struct {
        MatchId   string `json:"MatchId"`
        MatchName string `json:"MatchName"`
        Home      string // Поле для домашней команды
        Away      string // Поле для гостевой команды
    }

    // Парсинг сообщения
    if err := json.Unmarshal(msg, &parsedMsg); err != nil {
        log.Printf("[ERROR] Ошибка парсинга JSON из %s: %v | Сообщение: %s", name, err, string(msg))
        return
    }

    // Логируем распарсенные данные
    log.Printf("[DEBUG] Распарсенные данные: MatchId=%s, MatchName=%s", parsedMsg.MatchId, parsedMsg.MatchName)

    // Извлечение Home и Away из MatchName
    teams := splitMatchName(parsedMsg.MatchName, name)
    if len(teams) != 2 {
        log.Printf("[ERROR] Не удалось разделить MatchName на команды: %s", parsedMsg.MatchName)
        return
    }
    parsedMsg.Home = teams[0]
    parsedMsg.Away = teams[1]

    // Логируем извлечённые команды
    log.Printf("[DEBUG] Извлечённые команды: Home=%s, Away=%s", parsedMsg.Home, parsedMsg.Away)

    // Проверяем на пустые поля
    if parsedMsg.Home == "" || parsedMsg.Away == "" {
        log.Printf("[ERROR] Пустые данные команды: Home=%s, Away=%s", parsedMsg.Home, parsedMsg.Away)
        return
    }

    // Проверка наличия команд в базе
    log.Printf("[DEBUG] Проверяем наличие команды %s в %s", parsedMsg.Home, name)
    homeTeamExists := teamExists(name, parsedMsg.Home)
    log.Printf("[DEBUG] Проверяем наличие команды %s в %s", parsedMsg.Away, name)
    awayTeamExists := teamExists(name, parsedMsg.Away)

    // Добавляем команды, если их нет
    if !homeTeamExists {
        log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её", parsedMsg.Home, name)
        insertTeam(name, parsedMsg.Home)
    }
    if !awayTeamExists {
        log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её", parsedMsg.Away, name)
        insertTeam(name, parsedMsg.Away)
    }

    // Пытаемся связать команды (если это возможно)
    log.Printf("[DEBUG] Пытаемся связать команды для %s", name)
    if name == "Sansabet" {
        linkTeams(parsedMsg.Home, parsedMsg.Home)
        linkTeams(parsedMsg.Away, parsedMsg.Away)
    } else if name == "Pinnacle" {
        linkTeams(parsedMsg.Home, parsedMsg.Home)
        linkTeams(parsedMsg.Away, parsedMsg.Away)
    }

    log.Printf("[DEBUG] Завершение обработки сообщения из %s", name)
}


func linkTeams(sansabetTeam, pinnacleTeam string) {
    // Получаем ID команд
    var sansabetId, pinnacleId int
    if db == nil {
        log.Fatal("[ERROR] Подключение к базе данных не инициализировано")
    }

    err := db.QueryRow("SELECT id FROM sansabetTeams WHERE teamName = ? LIMIT 1", sansabetTeam).Scan(&sansabetId)
    if err != nil {
        log.Printf("[ERROR] Не удалось найти команду %s в Sansabet: %v", sansabetTeam, err)
        return
    }

    err = db.QueryRow("SELECT id FROM pinnacleTeams WHERE teamName = ? LIMIT 1", pinnacleTeam).Scan(&pinnacleId)
    if err != nil {
        log.Printf("[ERROR] Не удалось найти команду %s в Pinnacle: %v", pinnacleTeam, err)
        return
    }

    // Обновляем связь
    _, err = db.Exec("UPDATE sansabetTeams SET pinnacleId = ? WHERE id = ?", pinnacleId, sansabetId)
    if err != nil {
        log.Printf("[ERROR] Не удалось связать команды %s и %s: %v", sansabetTeam, pinnacleTeam, err)
        return
    }

    log.Printf("[DEBUG] Команды %s (Sansabet) и %s (Pinnacle) успешно связаны", sansabetTeam, pinnacleTeam)
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
    key := generateMatchKey(pair.MatchName, pair.MatchName) // Используйте home и away из данных
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

