package main

import (
    "encoding/json"
    "log"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "github.com/texttheater/golang-levenshtein/levenshtein"
)

// Типы данных
type MatchData struct {
    Source    string `json:"Source"`
    MatchId   string `json:"MatchId"`
    MatchName string `json:"MatchName"`
}

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
}

// Глобальные переменные
var matchesFromParser1 []MatchData
var matchesFromParser2 []MatchData
var matchesMutex sync.Mutex

var analyzerConnection *websocket.Conn

// Запуск мэтчинга
func startMatching() {
    log.Println("[Matching] Мэтчинг запущен")
    connectToAnalyzer()
}

func processMatchData(msg []byte) {
    var match MatchData
    if err := json.Unmarshal(msg, &match); err != nil {
        log.Printf("[Matching] Ошибка парсинга данных матча: %v", err)
        return
    }

    matchesMutex.Lock()
    defer matchesMutex.Unlock()

    if match.Source == "Sansabet" {
        matchesFromParser1 = append(matchesFromParser1, match)
    } else if match.Source == "Pinnacle" {
        matchesFromParser2 = append(matchesFromParser2, match)
    }
}


// Подключение к анализатору
func connectToAnalyzer() {
    for {
        log.Println("[Matching] Подключение к анализатору на ws://localhost:7400/matching")
        conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:7400/matching", nil)
        if err != nil {
            log.Printf("[Matching] Ошибка подключения к анализатору: %v. Повтор через 5 секунд.", err)
            time.Sleep(5 * time.Second)
            continue
        }
        analyzerConnection = conn
        log.Println("[Matching] Успешное подключение к анализатору")

        // Start the matching process
        go matchingProcess()

        // Receive match data from the analyzer
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Printf("[Matching] Ошибка чтения от анализатора: %v", err)
                break
            }

            // Process received match data
            processMatchData(msg)
        }

        conn.Close()
    }
}


// Функция для имитации получения данных от парсеров и мэтчинга
func matchingProcess() {
    for {
        time.Sleep(10 * time.Second)

        matchesMutex.Lock()
        matches1 := make([]MatchData, len(matchesFromParser1))
        matches2 := make([]MatchData, len(matchesFromParser2))
        copy(matches1, matchesFromParser1)
        copy(matches2, matchesFromParser2)
        matchesFromParser1 = nil
        matchesFromParser2 = nil
        matchesMutex.Unlock()

        pairs := findPairs(matches1, matches2)
        if len(pairs) > 0 {
            sendMatchesToAnalyzer(pairs)
        }
    }
}


// Функция для отправки пар в анализатор
func sendMatchesToAnalyzer(pairs []MatchPair) {
    data, err := json.Marshal(pairs)
    if err != nil {
        log.Printf("[Matching] Ошибка сериализации пар: %v", err)
        return
    }

    if analyzerConnection == nil {
        log.Println("[Matching] Нет соединения с анализатором")
        return
    }

    if err := analyzerConnection.WriteMessage(websocket.TextMessage, data); err != nil {
        log.Printf("[Matching] Ошибка отправки пар в анализатор: %v", err)
    } else {
        log.Printf("[Matching] Отправлены пары в анализатор: %s", string(data))
    }
}

// Поиск пар через Левенштейна
func findPairs(matches1, matches2 []MatchData) []MatchPair {
    var pairs []MatchPair
    uniquePairs := make(map[string]bool)

    log.Printf("[Matching] Начало создания пар. Количество матчей из Sansabet: %d, из Pinnacle: %d", len(matches1), len(matches2))

    for _, match1 := range matches1 {
        for _, match2 := range matches2 {
            // Проверим, что матчи из разных источников
            if match1.Source == match2.Source {
                continue
            }

            // Рассчитываем схожесть по Левенштейну
            score := levenshtein.DistanceForStrings([]rune(match1.MatchName), []rune(match2.MatchName), levenshtein.DefaultOptions)

            // Если схожесть меньше определенного порога
            if score <= 5 {
                pairKey := match1.MatchId + "_" + match2.MatchId

                if !uniquePairs[pairKey] {
                    uniquePairs[pairKey] = true
                    pairs = append(pairs, MatchPair{
                        SansabetId: match1.MatchId,
                        PinnacleId: match2.MatchId,
                        MatchName:  match1.MatchName,
                    })
                }
            }
        }
    }

    log.Printf("[Matching] Найдено пар: %d", len(pairs))
    return pairs
}

// Главная функция
func main() {
    startMatching()
    select {}
}
