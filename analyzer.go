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
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

const (
	maxStaleDuration = 5 * time.Second
	MARGIN           = 1.08
)

// Структуры данных
type ParsedMessage struct {
	MatchId    string
	MatchName  string
	Home       string
	Away       string
	LeagueName string // Название лиги
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

type MatchData struct {
	Data       string    // Содержит JSON-данные от букмекера
	LastUpdate time.Time // Время последнего обновления
}

// Глобальные переменные
var (
	db              *sql.DB
	lastSentMutex   sync.Mutex
	pairsMutex      sync.Mutex
	frontendMutex   sync.Mutex
	matchDataMutex  sync.RWMutex
	matchData       = map[string]map[string]*MatchData{"Sansabet": {}, "Pinnacle": {}}
	matchPairs      []MatchPair
	matchKeys       = make(map[string]bool) // Уникальные ключи матчей
	frontendClients = make(map[*websocket.Conn]bool)
	sansabetConns   = make(map[*websocket.Conn]bool)
	pinnacleConns   = make(map[*websocket.Conn]bool)
	upgrader        = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	extraPercents   = []struct {
		Min, Max, ExtraPercent float64
	}{
		{2.29, 2.75, 1.03},
		{2.75, 3.2, 1.04},
		{3.2, 3.7, 1.05},
	}
)

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
			sansabetConns[conn] = true
		} else if sourceName == "Pinnacle" {
			pinnacleConns[conn] = true
		}

		for {
			log.Printf("[DEBUG] Ожидание данных от %s", sourceName)
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("[ERROR] Ошибка чтения сообщения от %s: %v", sourceName, err)
				break
			}
			log.Printf("[DEBUG] Получены данные от %s: %s", sourceName, string(msg))
			saveMatchData(sourceName, msg)
		}

		if sourceName == "Sansabet" {
			delete(sansabetConns, conn)
		} else if sourceName == "Pinnacle" {
			delete(pinnacleConns, conn)
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
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[ERROR] Паника в saveMatchData: %v", r)
		}
	}()

	// Проверяем на пустое сообщение
	if len(msg) == 0 || string(msg) == "[]" {
		matchDataMutex.Lock()
		for key := range matchData[name] {
			delete(matchData[name], key)
			log.Printf("[DEBUG] Удален матч с ключом %s из источника %s из-за пустого массива", key, name)
		}
		matchDataMutex.Unlock()
		return
	}

	parsedMsg, err := parseMessage(name, msg)
	if err != nil {
		log.Printf("[ERROR] Ошибка парсинга сообщения от %s: %v", name, err)
		return
	}

	ensureTeamsExist(name, parsedMsg.Home, parsedMsg.Away, parsedMsg.LeagueName)
	if name == "Sansabet" {
		linked, linkedHome, linkedAway := linkTeamsForMatch(parsedMsg.Home, parsedMsg.Away)
		if linked {
			log.Printf("[DEBUG] Найдена связь для команд: Home=%s, Away=%s", linkedHome, linkedAway)
			updateMatchPairs(parsedMsg)
		} else {
			log.Printf("[DEBUG] Связь для команд Home=%s и Away=%s не найдена", parsedMsg.Home, parsedMsg.Away)
		}
	}

	key := generateMatchKey(parsedMsg.Home, parsedMsg.Away)

	matchDataMutex.Lock()
	matchData[name][key] = &MatchData{
		Data:       string(msg),
		LastUpdate: time.Now(),
	}
	matchDataMutex.Unlock()

	log.Printf("[DEBUG] Данные матча сохранены в matchData[%s][%s]", name, key)

	// Запускаем обработку пар
	processPairs()
	sendCurrentMatchesToFrontend()
}

// Отправка текущих матчей на фронтенд
func sendCurrentMatchesToFrontend() {
	results := groupResultsByMatch(matchPairs)
	forwardToFrontendBatch(results)
}

// Парсинг сообщения
func parseMessage(name string, msg []byte) (*ParsedMessage, error) {
	//log.Printf("[DEBUG] Получено сообщение из %s: %s", name, string(msg))

	var parsedMsg ParsedMessage
	if err := json.Unmarshal(msg, &parsedMsg); err != nil {
		return nil, fmt.Errorf("Ошибка парсинга JSON из %s: %v | Сообщение: %s", name, err, string(msg))
	}

	teams := splitMatchName(parsedMsg.MatchName, name)
	if len(teams) != 2 {
		return nil, fmt.Errorf("Не удалось разделить MatchName на команды: %s", parsedMsg.MatchName)
	}

	parsedMsg.Home = teams[0]
	parsedMsg.Away = teams[1]

	if parsedMsg.Home == "" || parsedMsg.Away == "" || parsedMsg.LeagueName == "" {
		return nil, fmt.Errorf("Недостаточно данных: Home=%s, Away=%s, LeagueName=%s", parsedMsg.Home, parsedMsg.Away, parsedMsg.LeagueName)
	}

	log.Printf("[DEBUG] Распарсенные данные: Home=%s, Away=%s, LeagueName=%s", parsedMsg.Home, parsedMsg.Away, parsedMsg.LeagueName)
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
func ensureTeamsExist(source, home, away, league string) {
	log.Printf("[DEBUG] Проверяем наличие команды %s в %s, лига: %s", home, source, league)
	if !teamExists(source, home, league) {
		log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её (лига: %s)", home, source, league)
		insertTeam(source, home, league)
	}

	log.Printf("[DEBUG] Проверяем наличие команды %s в %s, лига: %s", away, source, league)
	if !teamExists(source, away, league) {
		log.Printf("[DEBUG] Команда %s отсутствует в %s, добавляем её (лига: %s)", away, source, league)
		insertTeam(source, away, league)
	}
}

// Проверка наличия команды в базе данных
func teamExists(source, teamName, league string) bool {
	query := fmt.Sprintf("SELECT id FROM %sTeams WHERE teamName = ? AND leagueName = ? LIMIT 1", strings.ToLower(source))
	row := db.QueryRow(query, teamName, league)

	var id int
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[DEBUG] Команда %s отсутствует в %s для лиги %s", teamName, source, league)
			return false
		}
		log.Printf("[ERROR] Ошибка проверки команды %s в %s: %v", teamName, source, err)
		return false
	}
	log.Printf("[DEBUG] Команда %s найдена в %s для лиги %s", teamName, source, league)
	return true
}

// Вставка команды в базу данных
func insertTeam(source, teamName, league string) {
	const sport = "Soccer"

	log.Printf("[DEBUG] Вставка команды %s в %s (лига: %s, спорт: %s)", teamName, source, league, sport)

	query := fmt.Sprintf("INSERT INTO %sTeams(teamName, leagueName, sportName) VALUES (?, ?, ?)", strings.ToLower(source))
	_, err := db.Exec(query, teamName, league, sport)
	if err != nil {
		log.Printf("[ERROR] Ошибка вставки команды %s в %s: %v", teamName, source, err)
		return
	}
	log.Printf("[DEBUG] Команда %s успешно добавлена в %s (лига: %s, спорт: %s)", teamName, source, league, sport)
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

// Получение Pinnacle ID для команды
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

// Обновление пар матчей
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

	if pinnacleHomeId == 0 && pinnacleAwayId == 0 {
		log.Printf("[DEBUG] Нет связи с Pinnacle ни для одной из команд: %s vs %s", parsedMsg.Home, parsedMsg.Away)
		return
	}

	if pinnacleHomeId == 0 || pinnacleAwayId == 0 {
		log.Printf("[DEBUG] Необходим PinnacleId для одной или обеих команд, пытаемся получить отсутствующие данные")

		if pinnacleHomeId != 0 && pinnacleAwayId == 0 {
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

	if err := json.Unmarshal([]byte(pinnacleData.Data), &pinnacleMsg); err != nil {
		log.Printf("[ERROR] Ошибка парсинга данных Pinnacle: %v", err)
		return
	}

	matchParts := strings.SplitN(pinnacleMsg.MatchName, " vs ", 2)
	if len(matchParts) != 2 {
		log.Printf("[ERROR] Некорректный формат MatchName: %s", pinnacleMsg.MatchName)
		return
	}
	pinnacleHome := matchParts[0]
	pinnacleAway := matchParts[1]

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

	matchPairs = append(matchPairs, newPair)
	matchKeys[keySansabet] = true

	log.Printf("[DEBUG] Новая пара добавлена в matchPairs: %v", newPair)
}

// Поиск недостающей команды в matchData
func findMissingTeamInMatchData(knownTeam string) (string, error) {
	log.Printf("[DEBUG] Начинаем поиск недостающей команды с известной командой: %s", knownTeam)

	if pinnacleMatches, exists := matchData["Pinnacle"]; exists {
		log.Printf("[DEBUG] Найдены данные для источника Pinnacle, количество матчей: %d", len(pinnacleMatches))

		for key, matchJSON := range pinnacleMatches {
			log.Printf("[DEBUG] Обрабатываем матч с ключом: %s", key)

			var match struct {
				MatchName string `json:"MatchName"`
			}
			if err := json.Unmarshal([]byte(matchJSON.Data), &match); err != nil {
				log.Printf("[DEBUG] Ошибка парсинга JSON для ключа %s: %v", key, err)
				continue
			}

			log.Printf("[DEBUG] Успешно распарсен матч: %s", match.MatchName)

			teams := strings.Split(match.MatchName, " vs ")
			log.Printf("[DEBUG] Разделенные команды: %v", teams)

			if len(teams) == 2 {
				if teams[0] == knownTeam {
					log.Printf("[DEBUG] Известная команда найдена как первая, возвращаем вторую: %s", teams[1])
					return teams[1], nil
				} else if teams[1] == knownTeam {
					log.Printf("[DEBUG] Известная команда найдена как вторая, возвращаем первую: %s", teams[0])
					return teams[0], nil
				}
			} else {
				log.Printf("[DEBUG] Ошибка: Название матча имеет некорректный формат: %s", match.MatchName)
			}
		}
		log.Printf("[DEBUG] Матч с известной командой %s не найден в Pinnacle", knownTeam)
	} else {
		log.Printf("[DEBUG] Источник Pinnacle отсутствует в matchData")
	}

	return "", fmt.Errorf("команда %s не найдена в matchData", knownTeam)
}

// Получение названия команды по Pinnacle ID
func getTeamNameByPinnacleId(pinnacleId int) (string, error) {
	query := `SELECT teamName FROM pinnacleTeams WHERE id = ? LIMIT 1`
	var teamName string
	err := db.QueryRow(query, pinnacleId).Scan(&teamName)
	if err != nil {
		return "", err
	}
	return teamName, nil
}

// Получение Pinnacle ID напрямую по названию команды
func getPinnacleIdDirectly(teamName string) (int, error) {
	query := `SELECT id FROM pinnacleTeams WHERE teamName = ? LIMIT 1`
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

// Генерация ключа матча
func generateMatchKey(home, away string) string {
	const emptyHash = "da39a3ee5e6b4b0d3255bfef95601890afd80709"

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
	localMatchData := make(map[string]map[string]*MatchData)
	matchDataMutex.RLock()
	for source, matches := range matchData {
		localMatchData[source] = make(map[string]*MatchData)
		for key, match := range matches {
			localMatchData[source][key] = match
		}
	}
	matchDataMutex.RUnlock()

	// Удаление устаревших матчей
	for source, matches := range localMatchData {
		for key, match := range matches {
			if time.Since(match.LastUpdate) > maxStaleDuration {
				log.Printf("[DEBUG] Удаляем устаревший матч: Source=%s, Key=%s", source, key)
				matchDataMutex.Lock()
				delete(matchData[source], key)
				matchDataMutex.Unlock()
			}
		}
	}
	sendCurrentMatchesToFrontend()
}

// Группировка результатов по матчам
func groupResultsByMatch(pairs []MatchPair) []map[string]interface{} {
	results := []map[string]interface{}{}

	matchDataMutex.RLock()
	defer matchDataMutex.RUnlock()

	for _, pair := range pairs {
		keySansabet := generateMatchKey(pair.SansabetHome, pair.SansabetAway)
		keyPinnacle := generateMatchKey(pair.PinnacleHome, pair.PinnacleAway)

		sansabetMatch, sansabetExists := matchData["Sansabet"][keySansabet]
		pinnacleMatch, pinnacleExists := matchData["Pinnacle"][keyPinnacle]

		if sansabetExists && pinnacleExists &&
			time.Since(sansabetMatch.LastUpdate) <= maxStaleDuration &&
			time.Since(pinnacleMatch.LastUpdate) <= maxStaleDuration {

			result := processPairAndGetResult(pair)
			if result != nil {
				results = append(results, result)
			}
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

	commonOutcomes := findCommonOutcomes(sansabetData.Data, pinnacleData.Data)
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
	json.Unmarshal([]byte(sansabetData.Data), &sansabetParsed)
	json.Unmarshal([]byte(pinnacleData.Data), &pinnacleParsed)

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
		"MatchName":    pair.PinnacleName,
		"SansabetName": pair.SansabetName,
		"SansabetId":   pair.SansabetId,
		"PinnacleId":   pair.PinnacleId,
		"LeagueName":   leagueName,
		"Country":      country,
		"Outcomes":     filtered,
	}

	return result
}

// Нормализация ключа тотала
func normalizeTotal(total string) string {
	// Пробуем преобразовать строку в число
	value, err := strconv.ParseFloat(total, 64)
	if err != nil {
		return total // Если не удалось преобразовать, возвращаем как есть
	}
	// Возвращаем строку с одним знаком после запятой
	return fmt.Sprintf("%.1f", value)
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
		FirstTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"FirstTeamTotals"`
		SecondTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"SecondTeamTotals"`
		Handicap map[string]struct {
			Win1 float64 `json:"Win1"`
			Win2 float64 `json:"Win2"`
		} `json:"Handicap"`

		Time1Win1x2 map[string]float64 `json:"Time1Win1x2"`
		Time1Totals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time1Totals"`
		Time1FirstTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time1FirstTeamTotals"`
		Time1SecondTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time1SecondTeamTotals"`
		Time1Handicap map[string]struct {
			Win1 float64 `json:"Win1"`
			Win2 float64 `json:"Win2"`
		} `json:"Time1Handicap"`

		Time2Win1x2 map[string]float64 `json:"Time2Win1x2"`
		Time2Totals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time2Totals"`
		Time2FirstTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time2FirstTeamTotals"`
		Time2SecondTeamTotals map[string]struct {
			WinMore float64 `json:"WinMore"`
			WinLess float64 `json:"WinLess"`
		} `json:"Time2SecondTeamTotals"`
		Time2Handicap map[string]struct {
			Win1 float64 `json:"Win1"`
			Win2 float64 `json:"Win2"`
		} `json:"Time2Handicap"`
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

	// Создаем нормализованные мапы для тоталов
	normalizedSansaTotals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnTotals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})

	// Нормализуем ключи для основных тоталов
	for key, value := range sansabetOdds.Totals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTotals[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Totals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnTotals[normalizedKey] = value
	}

	// Проверяем тоталы с нормализованными ключами
	for key, sansabetTotal := range normalizedSansaTotals {
		if pinnacleTotal, exists := normalizedPinnTotals[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Totals Less: %s", key)
			}
		}
	}

	// Нормализуем ключи для индивидуальных тоталов первой команды
	normalizedSansaFirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnFirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})

	for key, value := range sansabetOdds.FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaFirstTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnFirstTeam[normalizedKey] = value
	}

	// Проверяем индивидуальные тоталы первой команды с нормализованными ключами
	for key, sansabetTotal := range normalizedSansaFirstTeam {
		if pinnacleTotal, exists := normalizedPinnFirstTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["First Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход First Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["First Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход First Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем ключи для индивидуальных тоталов второй команды
	normalizedSansaSecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnSecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})

	for key, value := range sansabetOdds.SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaSecondTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnSecondTeam[normalizedKey] = value
	}

	// Проверяем индивидуальные тоталы второй команды с нормализованными ключами
	for key, sansabetTotal := range normalizedSansaSecondTeam {
		if pinnacleTotal, exists := normalizedPinnSecondTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Second Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Second Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Second Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Second Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем ключи для гандикапов
	normalizedSansaHandicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})
	normalizedPinnHandicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})

	for key, value := range sansabetOdds.Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedSansaHandicap[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedPinnHandicap[normalizedKey] = value
	}

	// Проверяем гандикапы с нормализованными ключами
	for key, sansabetHandicap := range normalizedSansaHandicap {
		if pinnacleHandicap, exists := normalizedPinnHandicap[key]; exists {
			// Проверяем Win1
			if pinnacleHandicap.Win1 >= 1.02 && pinnacleHandicap.Win1 <= 35 {
				common["Handicap "+key+" Win1"] = [2]float64{sansabetHandicap.Win1, pinnacleHandicap.Win1}
				log.Printf("[DEBUG] Найден общий исход Handicap Win1: %s", key)
			}
			// Проверяем Win2
			if pinnacleHandicap.Win2 >= 1.02 && pinnacleHandicap.Win2 <= 35 {
				common["Handicap "+key+" Win2"] = [2]float64{sansabetHandicap.Win2, pinnacleHandicap.Win2}
				log.Printf("[DEBUG] Найден общий исход Handicap Win2: %s", key)
			}
		}
	}

	// Нормализуем тоталы первого тайма
	normalizedSansaTime1Totals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnTime1Totals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})

	for key, value := range sansabetOdds.Time1Totals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime1Totals[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time1Totals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnTime1Totals[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime1Totals {
		if pinnacleTotal, exists := normalizedPinnTime1Totals[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time1 Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time1 Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time1 Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time1 Totals Less: %s", key)
			}
		}
	}

	// Нормализуем индивидуальные тоталы первого тайма первой команды
	normalizedSansaTime1FirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnTime1FirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})

	for key, value := range sansabetOdds.Time1FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime1FirstTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time1FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnTime1FirstTeam[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime1FirstTeam {
		if pinnacleTotal, exists := normalizedPinnTime1FirstTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time1 First Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time1 First Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time1 First Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time1 First Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем индивидуальные тоталы первого тайма второй команды
	normalizedSansaTime1SecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnacleTime1SecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	for key, value := range sansabetOdds.Time1SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime1SecondTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time1SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime1SecondTeam[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime1SecondTeam {
		if pinnacleTotal, exists := normalizedPinnacleTime1SecondTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time1 Second Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time1 Second Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time1 Second Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time1 Second Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем гандикапы первого тайма
	normalizedSansaTime1Handicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})
	normalizedPinnacleTime1Handicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})
	for key, value := range sansabetOdds.Time1Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime1Handicap[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time1Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime1Handicap[normalizedKey] = value
	}

	for key, sansabetHandicap := range normalizedSansaTime1Handicap {
		if pinnacleHandicap, exists := normalizedPinnacleTime1Handicap[key]; exists {
			// Проверяем Win1
			if pinnacleHandicap.Win1 >= 1.02 && pinnacleHandicap.Win1 <= 35 {
				common["Time1 Handicap Win1 "+key] = [2]float64{sansabetHandicap.Win1, pinnacleHandicap.Win1}
				log.Printf("[DEBUG] Найден общий исход Time1 Handicap Win1: %s", key)
			}
			// Проверяем Win2
			if pinnacleHandicap.Win2 >= 1.02 && pinnacleHandicap.Win2 <= 35 {
				common["Time1 Handicap Win2 "+key] = [2]float64{sansabetHandicap.Win2, pinnacleHandicap.Win2}
				log.Printf("[DEBUG] Найден общий исход Time1 Handicap Win2: %s", key)
			}
		}
	}

	// Нормализуем тоталы второго тайма
	normalizedSansaTime2Totals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnacleTime2Totals := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	for key, value := range sansabetOdds.Time2Totals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime2Totals[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time2Totals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime2Totals[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime2Totals {
		if pinnacleTotal, exists := normalizedPinnacleTime2Totals[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time2 Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time2 Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time2 Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time2 Totals Less: %s", key)
			}
		}
	}

	// Нормализуем индивидуальные тоталы второго тайма первой команды
	normalizedSansaTime2FirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnacleTime2FirstTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	for key, value := range sansabetOdds.Time2FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime2FirstTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time2FirstTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime2FirstTeam[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime2FirstTeam {
		if pinnacleTotal, exists := normalizedPinnacleTime2FirstTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time2 First Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time2 First Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time2 First Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time2 First Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем индивидуальные тоталы второго тайма второй команды
	normalizedSansaTime2SecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	normalizedPinnacleTime2SecondTeam := make(map[string]struct {
		WinMore float64 `json:"WinMore"`
		WinLess float64 `json:"WinLess"`
	})
	for key, value := range sansabetOdds.Time2SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime2SecondTeam[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time2SecondTeamTotals {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime2SecondTeam[normalizedKey] = value
	}

	for key, sansabetTotal := range normalizedSansaTime2SecondTeam {
		if pinnacleTotal, exists := normalizedPinnacleTime2SecondTeam[key]; exists {
			// Проверяем WinMore
			if pinnacleTotal.WinMore >= 1.02 && pinnacleTotal.WinMore <= 35 {
				common["Time2 Second Team Total More "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
				log.Printf("[DEBUG] Найден общий исход Time2 Second Team Totals More: %s", key)
			}
			// Проверяем WinLess
			if pinnacleTotal.WinLess >= 1.02 && pinnacleTotal.WinLess <= 35 {
				common["Time2 Second Team Total Less "+key] = [2]float64{sansabetTotal.WinLess, pinnacleTotal.WinLess}
				log.Printf("[DEBUG] Найден общий исход Time2 Second Team Totals Less: %s", key)
			}
		}
	}

	// Нормализуем гандикапы второго тайма
	normalizedSansaTime2Handicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})
	normalizedPinnacleTime2Handicap := make(map[string]struct {
		Win1 float64 `json:"Win1"`
		Win2 float64 `json:"Win2"`
	})
	for key, value := range sansabetOdds.Time2Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedSansaTime2Handicap[normalizedKey] = value
	}
	for key, value := range pinnacleOdds.Time2Handicap {
		normalizedKey := normalizeTotal(key)
		normalizedPinnacleTime2Handicap[normalizedKey] = value
	}

	for key, sansabetHandicap := range normalizedSansaTime2Handicap {
		if pinnacleHandicap, exists := normalizedPinnacleTime2Handicap[key]; exists {
			// Проверяем Win1
			if pinnacleHandicap.Win1 >= 1.02 && pinnacleHandicap.Win1 <= 35 {
				common["Time2 Handicap Win1 "+key] = [2]float64{sansabetHandicap.Win1, pinnacleHandicap.Win1}
				log.Printf("[DEBUG] Найден общий исход Time2 Handicap Win1: %s", key)
			}
			// Проверяем Win2
			if pinnacleHandicap.Win2 >= 1.02 && pinnacleHandicap.Win2 <= 35 {
				common["Time2 Handicap Win2 "+key] = [2]float64{sansabetHandicap.Win2, pinnacleHandicap.Win2}
				log.Printf("[DEBUG] Найден общий исход Time2 Handicap Win2: %s", key)
			}
		}
	}

	//log.Printf("[DEBUG] Общие исходы: %+v", common)
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
		log.Printf("[DEBUG] Отправка данных клиенту: %v", client.RemoteAddr())
		log.Printf("[DEBUG] Данные для отправки клиенту: %s", string(data))
		if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[ERROR] Ошибка отправки данных клиенту (%v): %v", client.RemoteAddr(), err)
			client.Close()
			delete(frontendClients, client)
			log.Printf("[DEBUG] Клиент удалён: %v", client.RemoteAddr())
		} else {
			log.Printf("[DEBUG] Данные успешно отправлены клиенту: %v", client.RemoteAddr())
		}
	}
}

// Главная функция
func main() {
	initDB()
	startAnalyzer()
	select {}
}
