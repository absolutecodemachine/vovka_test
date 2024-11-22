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

var matchDataTTL = map[string]map[string]time.Time{
    "Sansabet": make(map[string]time.Time),
    "Pinnacle": make(map[string]time.Time),
}

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
    SansabetId   string `json:"SansabetId"`
    PinnacleId   string `json:"PinnacleId"`
    SansabetName string // Название матча от Sansabet
    PinnacleName string // Название матча от Pinnacle
    SansabetHome string // Название домашней команды от Sansabet
    SansabetAway string // Название гостевой команды от Sansabet
    PinnacleHome string // Название домашней команды от Pinnacle
    PinnacleAway string // Название гостевой команды от Pinnacle
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

    if name == "Sansabet" {
        linked, linkedHome, linkedAway := linkTeamsForMatch(parsedMsg.Home, parsedMsg.Away)
        if linked {
            log.Printf("[DEBUG] Найдена связь для команд: Home=%s, Away=%s", linkedHome, linkedAway)
            updateMatchPairs(parsedMsg)
        }

    }
    key := generateMatchKey(parsedMsg.Home, parsedMsg.Away)
    // Удаление устаревших данных для текущего ключа
    if _, exists := matchData[name][key]; exists {
        delete(matchData[name], key)
        delete(matchDataTTL[name], key)
        log.Printf("[DEBUG] Удалены старые данные: Source=%s, Key=%s", name, key)
    }

    matchData[name][key] = string(msg)
    matchDataTTL[name][key] = time.Now()

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
func linkTeamsForMatch(home, away string) (bool, string, string) {
    log.Printf("[DEBUG] Пытаемся найти связи для матча: Home=%s, Away=%s", home, away)

    homeLinked := linkTeam(home)
    awayLinked := linkTeam(away)

    linkedHome := ""
    linkedAway := ""

    if homeLinked {
        linkedHome = home
    }
    if awayLinked {
        linkedAway = away
    }
    // здесь видимо не дописана логика ммм
    if homeLinked || awayLinked {
        log.Printf("[DEBUG] Найдена связь: HomeLinked=%t, AwayLinked=%t", homeLinked, awayLinked)
        return true, linkedHome, linkedAway
    }

    log.Printf("[DEBUG] Связи не найдены для обеих команд: Home=%s, Away=%s", home, away)
    return false, linkedHome, linkedAway
}

// Проверяем связь для одной команды
func linkTeam(teamName string) bool {
    log.Printf("[DEBUG] Проверяем связь для команды %s в sansabetTeams", teamName)

    query := `SELECT pinnacleId FROM sansabetTeams WHERE teamName = ? AND pinnacleId > 0 LIMIT 1`
    var linkedId int

    err := db.QueryRow(query, teamName).Scan(&linkedId)
    if err != nil {
        if err == sql.ErrNoRows {
            log.Printf("[DEBUG] Связь не найдена для команды %s", teamName)
        } else {
            log.Printf("[ERROR] Ошибка запроса для команды %s: %v", teamName, err)
        }
        return false
    }

    log.Printf("[DEBUG] Связь найдена для команды %s: pinnacleId=%d", teamName, linkedId)
    return true
}


// getPinnacleIdByTeamName получает Pinnacle ID напрямую из таблицы pinnacleTeams по имени команды
func getPinnacleIdByTeamName(teamName string) (int, error) {
    query := "SELECT id FROM pinnacleTeams WHERE teamName = ? LIMIT 1"
    var pinnacleId int
    err := db.QueryRow(query, teamName).Scan(&pinnacleId)
    if err != nil {
        if err == sql.ErrNoRows {
            return 0, fmt.Errorf("нет команды Pinnacle с именем %s", teamName)
        }
        return 0, err
    }
    return pinnacleId, nil
}

// вызываем только для сансы, имей ввиду
func updateMatchPairs(parsedMsg *ParsedMessage) {
    keySansabet := generateMatchKey(parsedMsg.Home, parsedMsg.Away)
    pairsMutex.Lock()
    defer pairsMutex.Unlock()

    if _, exists := matchKeys[keySansabet]; exists {
        log.Printf("[DEBUG] Матч уже существует: %s vs %s", parsedMsg.Home, parsedMsg.Away)
        return
    }

    pinnacleHomeId, err := getTeamPinnacleId(parsedMsg.Home)
    if err != nil && err != sql.ErrNoRows {
        log.Printf("[ERROR] Ошибка получения Pinnacle ID для команды Home: %v", err)
        return
    }

    pinnacleAwayId, err := getTeamPinnacleId(parsedMsg.Away)
    if err != nil && err != sql.ErrNoRows {
        log.Printf("[ERROR] Ошибка получения Pinnacle ID для команды Away: %v", err)
        return
    }

    // Проверяем, есть ли связь хотя бы для одной команды
    if pinnacleHomeId == 0 && pinnacleAwayId == 0 {
        log.Printf("[DEBUG] Нет связи с Pinnacle ни для одной из команд: %s vs %s", parsedMsg.Home, parsedMsg.Away)
        return
    }

    // Проверяем, есть ли обе команды после получения ID
    if pinnacleHomeId == 0 || pinnacleAwayId == 0 {
        log.Printf("[DEBUG] Необходим PinnacleId для одной или обеих команд, пытаемся получить отсутствующие данные")

        if pinnacleHomeId != 0 && pinnacleAwayId == 0 {
            // Есть Home, ищем Away
            knownTeamName, err := getTeamNameByPinnacleId(pinnacleHomeId)
            if err != nil {
                log.Printf("[ERROR] Ошибка получения названия команды по Pinnacle ID Home: %d. Ошибка: %v", pinnacleHomeId, err)
                return
            }

            missingTeamName, err := findMissingTeamInMatchData(knownTeamName)
            if err != nil {
                log.Printf("[ERROR] Не удалось найти недостающую команду в matchData: %v", err)
                return
            }

            pinnacleAwayId, err = getPinnacleIdDirectly(missingTeamName)
            if err != nil {
                log.Printf("[ERROR] Ошибка получения Pinnacle ID для недостающей команды Away: %s. Ошибка: %v", missingTeamName, err)
                return
            }
        }

        if pinnacleAwayId != 0 && pinnacleHomeId == 0 {
            // Есть Away, ищем Home
            knownTeamName, err := getTeamNameByPinnacleId(pinnacleAwayId)
            if err != nil {
                log.Printf("[ERROR] Ошибка получения названия команды по Pinnacle ID Away: %d. Ошибка: %v", pinnacleAwayId, err)
                return
            }

            missingTeamName, err := findMissingTeamInMatchData(knownTeamName)
            if err != nil {
                log.Printf("[ERROR] Не удалось найти недостающую команду в matchData: %v", err)
                return
            }

            pinnacleHomeId, err = getPinnacleIdDirectly(missingTeamName)
            if err != nil {
                log.Printf("[ERROR] Ошибка получения Pinnacle ID для недостающей команды Home: %s. Ошибка: %v", missingTeamName, err)
                return
            }
        }

        // Финальная проверка: если после всех попыток ID всё ещё отсутствуют, выходим
        if pinnacleHomeId == 0 || pinnacleAwayId == 0 {
            log.Printf("[ERROR] Не удалось получить обе команды для матча после попыток: HomeId=%d, AwayId=%d", pinnacleHomeId, pinnacleAwayId)
            return
        }
    }


    linkedHome, err := getTeamNameByPinnacleId(pinnacleHomeId)
    if err != nil {
        log.Printf("[ERROR] Ошибка получения названия команды Home по Pinnacle ID: %d. Ошибка: %v", pinnacleHomeId, err)
        return
    }

    linkedAway, err := getTeamNameByPinnacleId(pinnacleAwayId)
    if err != nil {
        log.Printf("[ERROR] Ошибка получения названия команды Away по Pinnacle ID: %d. Ошибка: %v", pinnacleAwayId, err)
        return
    }

    keyPinnacle := generateMatchKey(linkedHome, linkedAway)

    pinnacleData, pinnacleExists := matchData["Pinnacle"][keyPinnacle]
    if !pinnacleExists {
        log.Printf("[DEBUG] Отсутствуют данные от Pinnacle для матча: %s", parsedMsg.MatchName)
        return
    }

    var pinnacleMsg struct {
        MatchId   string `json:"MatchId"`
        MatchName string `json:"MatchName"`
    }

    log.Printf("[ZAEB] This is pinnacleData: %s", pinnacleData)
    if err := json.Unmarshal([]byte(pinnacleData), &pinnacleMsg); err != nil {
        log.Printf("[ERROR] Ошибка парсинга данных Pinnacle: %v", err)
        return
    }

    // Разделяем MatchName на Home и Away
    matchParts := strings.SplitN(pinnacleMsg.MatchName, " vs ", 2)
    if len(matchParts) != 2 {
        log.Printf("[ERROR] Некорректный формат MatchName: %s", pinnacleMsg.MatchName)
        return
    }
    pinnacleHome := matchParts[0]
    pinnacleAway := matchParts[1]

    // Создаём новую пару
    newPair := MatchPair{
        SansabetId:   parsedMsg.MatchId,
        SansabetName: parsedMsg.MatchName,
        SansabetHome: parsedMsg.Home,
        SansabetAway: parsedMsg.Away,
        PinnacleId:   pinnacleMsg.MatchId,
        PinnacleName: pinnacleMsg.MatchName,
        PinnacleHome: pinnacleHome,
        PinnacleAway: pinnacleAway,
    }

    log.Printf("[INFO] New pair created: %+v", newPair)

    matchPairs = append(matchPairs, newPair)
    matchKeys[keySansabet] = true

    log.Printf("[DEBUG] Новая пара добавлена в matchPairs: %v", newPair)
}


func findMissingTeamInMatchData(knownTeam string) (string, error) {
    log.Printf("[DRON] Начинаем поиск недостающей команды с известной командой: %s", knownTeam)
    //log.Printf("[DRON] all matchData ", matchData)
    if pinnacleMatches, exists := matchData["Pinnacle"]; exists {
        log.Printf("[DRON] Найдены данные для источника Pinnacle, количество матчей: %d", len(pinnacleMatches))
        
        for key, matchJSON := range pinnacleMatches {
            log.Printf("[DRON] Обрабатываем матч с ключом: %s", key)
            
            var match struct {
                MatchName string `json:"MatchName"`
            }
            if err := json.Unmarshal([]byte(matchJSON), &match); err != nil {
                log.Printf("[DRON] Ошибка парсинга JSON для ключа %s: %v", key, err)
                continue
            }

            log.Printf("[DRON] Успешно распарсен матч: %s", match.MatchName)
            
            teams := strings.Split(match.MatchName, " vs ")
            log.Printf("[DRON] Разделенные команды: %v", teams)
            
            if len(teams) == 2 {
                if teams[0] == knownTeam {
                    log.Printf("[DRON] Известная команда найдена как первая, возвращаем вторую: %s", teams[1])
                    return teams[1], nil
                } else if teams[1] == knownTeam {
                    log.Printf("[DRON] Известная команда найдена как вторая, возвращаем первую: %s", teams[0])
                    return teams[0], nil
                }
            } else {
                log.Printf("[DRON] Ошибка: Название матча имеет некорректный формат: %s", match.MatchName)
            }
        }
        log.Printf("[DRON] Матч с известной командой %s не найден в Pinnacle", knownTeam)
    } else {
        log.Printf("[DRON] Источник Pinnacle отсутствует в matchData")
    }

    return "", fmt.Errorf("команда %s не найдена в matchData", knownTeam)
}

func getTeamNameByPinnacleId(pinnacleId int) (string, error) {
    query := `
        SELECT teamName
        FROM pinnacleTeams
        WHERE id = ?
        LIMIT 1
    `

    var teamName string
    err := db.QueryRow(query, pinnacleId).Scan(&teamName)
    if err != nil {
        return "", err
    }
    return teamName, nil
}


func getPinnacleIdDirectly(teamName string) (int, error) {
    query := `
        SELECT id
        FROM pinnacleTeams
        WHERE teamName = ?
        LIMIT 1
    `
    var pinnacleId int
    err := db.QueryRow(query, teamName).Scan(&pinnacleId)
    if err != nil {
        if err == sql.ErrNoRows {
            log.Printf("[DEBUG] Команда %s отсутствует в pinnacleTeams", teamName)
            return 0, fmt.Errorf("команда %s не найдена в pinnacleTeams", teamName)
        }
        log.Printf("[ERROR] Ошибка выполнения запроса для команды %s: %v", teamName, err)
        return 0, err
    }

    log.Printf("[DEBUG] Найден Pinnacle ID для команды %s: %d", teamName, pinnacleId)
    return pinnacleId, nil
}


func getTeamPinnacleId(teamName string) (int, error) {
    query := `
        SELECT sansabetTeams.pinnacleId
        FROM sansabetTeams
        WHERE sansabetTeams.teamName = ?
        LIMIT 1
    `
    var pinnacleId int
    err := db.QueryRow(query, teamName).Scan(&pinnacleId)
    if err != nil {
        return 0, err
    }
    return pinnacleId, nil
}


func getPinnacleTeams(pinnacleId int) (string, string, error) {
    query := `
        SELECT teamName
        FROM pinnacleTeams
        WHERE id = ?
    `
    var teamName string
    err := db.QueryRow(query, pinnacleId).Scan(&teamName)
    if err != nil {
        return "", "", err
    }

    // Логи
    log.Printf("[DEBUG] Название команды Pinnacle для id=%d: %s", pinnacleId, teamName)

    // Разбиваем название команды Pinnacle на Home и Away
    parts := strings.Split(teamName, " vs ")
    if len(parts) != 2 {
        log.Printf("[ERROR] Неверный формат названия команды Pinnacle: %s", teamName)
        return "", "", fmt.Errorf("Неверный формат названия команды Pinnacle: %s", teamName)
    }

    // Логи
    log.Printf("[DEBUG] Разделённые команды Pinnacle: Home=%s, Away=%s", parts[0], parts[1])

    return parts[0], parts[1], nil
}


// Генерация ключа матча
func generateMatchKey(home, away string) string {
    const emptyHash = "da39a3ee5e6b4b0d3255bfef95601890afd80709"
    
    log.Printf("[generateMatchKey] team 1 and team 2: %s, %s", home, away)
    h1 := sha1.Sum([]byte(home))
    h2 := sha1.Sum([]byte(away))
    
    hexH1 := hex.EncodeToString(h1[:])
    hexH2 := hex.EncodeToString(h2[:])
    
    if hexH1 == emptyHash || hexH2 == emptyHash {
        log.Printf("[WARNING] One of the teams has an empty hash: home hash = %s, away hash = %s", hexH1, hexH2)
    }
    
    return hexH1 + hexH2
}


// Обработка пар матчей
func processPairs() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        // Удаление устаревших данных
        for source, keys := range matchDataTTL {
            for key, timestamp := range keys {
                if time.Since(timestamp) > 60*time.Second { // TTL = 10 секунд CHANGE!
                    delete(matchData[source], key)
                    delete(matchDataTTL[source], key)
                    log.Printf("[DEBUG] Удалены устаревшие данные: Source=%s, Key=%s", source, key)
                }
            }
        }

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
    keySansabet := generateMatchKey(pair.SansabetHome, pair.SansabetAway)
    keyPinnacle := generateMatchKey(pair.PinnacleHome, pair.PinnacleAway)

    sansabetData, sansabetExists := matchData["Sansabet"][keySansabet]
    pinnacleData, pinnacleExists := matchData["Pinnacle"][keyPinnacle]
    // но здесь матчдата содержит и пиннаклдату и сансу
    if !sansabetExists || !pinnacleExists {
        if !sansabetExists {
            log.Printf("[DEBUG] Нет данных для Sansabet: ключ %s", keySansabet)
        }
        if !pinnacleExists {
            log.Printf("[DEBUG] Нет данных для Pinnacle: ключ %s", keyPinnacle)
        }
        return nil
    }

    log.Printf("[DEBUG] Анализируем пару: %s | SansabetId=%s | PinnacleId=%s", pair.PinnacleName, pair.SansabetId, pair.PinnacleId)

    commonOutcomes := findCommonOutcomes(sansabetData, pinnacleData)
    if len(commonOutcomes) == 0 {
        log.Printf("[DEBUG] Нет общих исходов для пары: %s", pair.SansabetName)
        return nil
    }

    filtered := calculateAndFilterCommonOutcomes(commonOutcomes)
    if len(filtered) == 0 {
        log.Printf("[DEBUG] Нет подходящих исходов для пары: %s", pair.SansabetName)
        return nil
    }

    var sansabetParsed, pinnacleParsed map[string]interface{}
    json.Unmarshal([]byte(sansabetData), &sansabetParsed)
    json.Unmarshal([]byte(pinnacleData), &pinnacleParsed)

    leagueName := ""
    if leagueNamePinnacle, ok := pinnacleParsed["LeagueName"].(string); ok && leagueNamePinnacle != "" {
        leagueName = leagueNamePinnacle
    } else if leagueNameSansabet, ok := sansabetParsed["LeagueName"].(string); ok {
        leagueName = leagueNameSansabet
    }

    country := ""
    if countryPinnacle, ok := pinnacleParsed["Country"].(string); ok {
        country = countryPinnacle
    } else if countrySansabet, ok := sansabetParsed["Country"].(string); ok {
        country = countrySansabet
    }

    result := map[string]interface{}{
        "MatchName":  pair.PinnacleName, // Используем имя Pinnacle
        "SansabetId": pair.SansabetId,
        "PinnacleId": pair.PinnacleId,
        "LeagueName": leagueName,
        "Country":    country,
        "Outcomes":   filtered,
    }

    return result
}


// Поиск общих исходов
func findCommonOutcomes(sansabetData, pinnacleData string) map[string][2]float64 {
    //log.Printf("[DEBUG] Входные данные для анализа: Sansabet=%s, Pinnacle=%s", sansabetData, pinnacleData)

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
        if pinnacleValue, exists := pinnacleOdds.Win1x2[key]; exists && pinnacleValue >= 1.02 && pinnacleValue <= 35 {
            common[key] = [2]float64{sansabetValue, pinnacleValue}
            log.Printf("[DEBUG] Найден общий исход Win1x2: %s", key)
        }
    }

    for key, sansabetTotal := range sansabetOdds.Totals {
        if pinnacleTotal, exists := pinnacleOdds.Totals[key]; exists && pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
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
func calculateAndFilterCommonOutcomes(commonOutcomes map[string][2]float64) []map[string]interface{} {
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
    // Сортируем результаты по убыванию ROI
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