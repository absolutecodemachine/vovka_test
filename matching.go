package main

import (
    "encoding/json"
    "log"
    //"strings"
    //"fmt"
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
    Pid       int64  `json:"Pid"` // Новое поле
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
    go connectToAnalyzer()
    go connectParser("ws://localhost:7100", "Pinnacle", &matchesFromParser2)
    go connectParser("ws://localhost:7200", "Sansabet", &matchesFromParser1)
    go processMatches()
}

// Подключение к анализатору
func connectToAnalyzer() {
    for {
        log.Println("[Matching] Подключение к анализатору на ws://localhost:7400")
        conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:7400", nil)
        if err != nil {
            log.Printf("[Matching] Ошибка подключения к анализатору: %v. Повтор через 5 секунд.", err)
            time.Sleep(5 * time.Second)
            continue
        }
        analyzerConnection = conn
        log.Println("[Matching] Успешное подключение к анализатору")
        break
    }
}

// Подключение к парсеру
func connectParser(url, source string, matchList *[]MatchData) {
    for {
        log.Printf("[Matching] Подключение к парсеру %s на %s", source, url)
        conn, _, err := websocket.DefaultDialer.Dial(url, nil)
        if err != nil {
            log.Printf("[Matching] Ошибка подключения к %s: %v. Повтор через 5 секунд.", source, err)
            time.Sleep(5 * time.Second)
            continue
        }
        defer conn.Close()

        log.Printf("[Matching] Успешное подключение к парсеру %s", source)
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Printf("[Matching] Ошибка чтения от %s: %v. Переподключение.", source, err)
                break
            }

            // Распарсить сообщение и извлечь данные
            var match MatchData
            if err := json.Unmarshal(msg, &match); err != nil {
                log.Printf("[Matching] Ошибка парсинга сообщения от %s: %v", source, err)
                continue
            }

            // Явно присваиваем Source
            match.Source = source
            log.Printf("[Matching] Получен матч: %+v", match)

            matchesMutex.Lock()
            *matchList = append(*matchList, match)
            matchesMutex.Unlock()
        }
    }
}



// Обработка матчей и отправка пар
func processMatches() {
    for {
        time.Sleep(1 * time.Second)

        matchesMutex.Lock()
        if len(matchesFromParser1) > 0 && len(matchesFromParser2) > 0 {
            log.Printf("[Matching] Матчи из Sansabet: %+v", matchesFromParser1)
            log.Printf("[Matching] Матчи из Pinnacle: %+v", matchesFromParser2)

            pairs := findPairs(matchesFromParser1, matchesFromParser2)
            log.Printf("[Matching] Найденные пары: %+v", pairs)

            sendPairsToAnalyzer(pairs)
        } else {
            log.Println("[Matching] Недостаточно данных для формирования пар.")
        }
        matchesMutex.Unlock()
    }
}




// Поиск пар через Левенштейна
func findPairs(matches1, matches2 []MatchData) []MatchPair {
    var pairs []MatchPair
    uniquePairs := make(map[string]bool)

    log.Printf("[Matching] Начало создания пар. Количество матчей из Sansabet: %d, из Pinnacle: %d", len(matches1), len(matches2))

    for i, match1 := range matches1 {
        log.Printf("[Matching] Обработка матча из Sansabet #%d: %+v", i, match1)
        for j, match2 := range matches2 {
            log.Printf("[Matching] Сравнение с матчем из Pinnacle #%d: %+v", j, match2)

            // Логируем значения Source для проверки
            log.Printf("[Matching] Source match1: '%s', Source match2: '%s'", match1.Source, match2.Source)

            // Проверим, что матчи из разных источников
            if match1.Source == match2.Source {
                log.Printf("[Matching] Пропуск пары: одинаковый источник %s", match1.Source)
                continue
            }

            // Рассчитываем схожесть по Левенштейну
            score := levenshtein.DistanceForStrings([]rune(match1.MatchName), []rune(match2.MatchName), levenshtein.DefaultOptions)
            log.Printf("[Matching] Результат сравнения: '%s' ↔ '%s', Score: %d", match1.MatchName, match2.MatchName, score)

            // Если схожесть больше 65
            if score >= 65 {
                pairKey := match1.MatchId + "_" + match2.MatchId
                log.Printf("[Matching] Генерация ключа пары: %s", pairKey)

                if !uniquePairs[pairKey] {
                    log.Printf("[Matching] Добавляем пару: %s ↔ %s", match1.MatchName, match2.MatchName)
                    uniquePairs[pairKey] = true
                    pairs = append(pairs, MatchPair{
                        SansabetId: match1.MatchId,
                        PinnacleId: match2.MatchId,
                        MatchName:  match1.MatchName + " ↔ " + match2.MatchName,
                    })
                } else {
                    log.Printf("[Matching] Пара уже существует: %s ↔ %s", match1.MatchName, match2.MatchName)
                }
            } else {
                log.Printf("[Matching] Пара отклонена (Score < 65): %s ↔ %s", match1.MatchName, match2.MatchName)
            }
        }
    }

    log.Printf("[Matching] Найдено пар: %d", len(pairs))
    return pairs
}


// Расчёт процента схожести
func similarity(str1, str2 string) float64 {
    distance := levenshtein.DistanceForStrings([]rune(str1), []rune(str2), levenshtein.DefaultOptions)
    maxLen := float64(len(str1))
    if len(str2) > len(str1) {
        maxLen = float64(len(str2))
    }
    return (1 - float64(distance)/maxLen) * 100
}

// Отправка пар в анализатор
func sendPairsToAnalyzer(pairs []MatchPair) {
    log.Printf("[Matching] Отправляем пары в анализатор: %v", pairs)
    if analyzerConnection == nil {
        log.Println("[Matching] Нет соединения с анализатором")
        return
    }

    data, err := json.Marshal(pairs)
    if err != nil {
        log.Printf("[Matching] Ошибка сериализации пар: %v", err)
        return
    }

    if err := analyzerConnection.WriteMessage(websocket.TextMessage, data); err != nil {
        log.Printf("[Matching] Ошибка отправки пар в анализатор: %v", err)
    }
}

// Главная функция
func main() {
    startMatching()
    select {}
}
