package main

import (
    "encoding/json"
    "log"
    "strings"
    "sync"
    "time"

    "github.com/gorilla/websocket"
    "github.com/texttheater/golang-levenshtein/levenshtein"
)

// Тип данных, ожидаемых от парсеров
type OneGame struct {
    MatchName string `json:"MatchName"`
}

// Структура для хранения матча с указанием источника
type MatchData struct {
    Source    string // Источник данных
    MatchName string // Название матча
}

// Глобальные списки для хранения матчей
var matchesFromParser1 []MatchData
var matchesFromParser2 []MatchData

// Мьютекс для защиты списков
var matchesMutex sync.Mutex

// Соединение с анализатором
var analyzerConnection *websocket.Conn
var analyzerMutex = sync.Mutex{}

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
        analyzerMutex.Lock()
        analyzerConnection = conn
        analyzerMutex.Unlock()
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

            // Распарсить сообщение и извлечь название матча
            var game OneGame
            if err := json.Unmarshal(msg, &game); err != nil {
                log.Printf("[Matching] Некорректное сообщение от %s: %s. Ошибка: %v", source, string(msg), err)
                continue
            }

            if source == "Pinnacle" && strings.Contains(strings.ToLower(game.MatchName), "(corners)") {
                log.Printf("[Matching] Пропуск матча с '(corners)': %s", game.MatchName)
                continue
            }

            log.Printf("[Matching] Получен матч от %s: %s", source, game.MatchName)

            // Добавляем матч в список с указанием источника
            matchesMutex.Lock()
            *matchList = append(*matchList, MatchData{
                Source:    source,
                MatchName: game.MatchName,
            })
            matchesMutex.Unlock()
        }
    }
}

// Функция расчёта процента схожести
func similarity(str1, str2 string) float64 {
    distance := levenshtein.DistanceForStrings([]rune(str1), []rune(str2), levenshtein.DefaultOptions)
    maxLen := float64(len(str1))
    if len(str2) > len(str1) {
        maxLen = float64(len(str2))
    }
    return (1 - float64(distance)/maxLen) * 100
}

// Обработка и отправка матчей в анализатор
func processMatches() {
    for {
        time.Sleep(1 * time.Second) // Пауза для упорядоченной обработки

        matchesMutex.Lock()
        if len(matchesFromParser1) > 0 && len(matchesFromParser2) > 0 {
            // Ищем пары
            pairs := findPairs(matchesFromParser1, matchesFromParser2)

            // Логируем и отправляем пары
            for _, pair := range pairs {
                forwardToAnalyzer(pair.MatchName)
            }
        }
        matchesMutex.Unlock()
    }
}

// Поиск пар через Левенштейна
func findPairs(matches1, matches2 []MatchData) []MatchData {
    var pairs []MatchData
    uniquePairs := make(map[string]bool) // Для хранения уникальных пар

    for _, match1 := range matches1 {
        for _, match2 := range matches2 {
            // Проверяем, что матчи из разных источников
            if match1.Source == match2.Source {
                continue
            }

            // Рассчитываем схожесть по Левенштейну
            similarityScore := similarity(match1.MatchName, match2.MatchName)
            if similarityScore >= 65 {
                pairKey := match1.MatchName + " ↔ " + match2.MatchName
                if !uniquePairs[pairKey] {
                    uniquePairs[pairKey] = true
                    log.Printf("[Matching] Найдена пара: %s (Схожесть: %.2f%%)", pairKey, similarityScore)

                    // Добавляем пару в список для анализа
                    pairs = append(pairs, MatchData{
                        MatchName: pairKey,
                    })
                }
            }
        }
    }
    return pairs
}

// Пересылка данных в анализатор
func forwardToAnalyzer(matchName string) {
    analyzerMutex.Lock()
    defer analyzerMutex.Unlock()

    if analyzerConnection != nil {
        log.Printf("[Matching] Отправка в анализатор: %s", matchName)
        if err := analyzerConnection.WriteMessage(websocket.TextMessage, []byte(matchName)); err != nil {
            log.Printf("[Matching] Ошибка отправки данных в анализатор: %v", err)
            analyzerConnection = nil
        }
    } else {
        log.Printf("[Matching] Пропуск отправки в анализатор: нет соединения")
    }
}

// Главная функция
func main() {
    startMatching()
    select {} // Бесконечный цикл, чтобы процесс продолжал работать
}
