package main

import (
    "encoding/json"
    "log"
    "sync"
    "time"

    "github.com/gorilla/websocket"
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
    go connectParser("ws://localhost:7100", "Sansabet", &matchesFromParser1)
    go connectParser("ws://localhost:7200", "Pinnacle", &matchesFromParser2)
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

// Обработка и отправка матчей в анализатор
func processMatches() {
    for {
        time.Sleep(1 * time.Second) // Пауза для упорядоченной отправки данных

        // Сначала отправляем матчи от первого парсера
        matchesMutex.Lock()
        if len(matchesFromParser1) > 0 {
            for _, match := range matchesFromParser1 {
                forwardToAnalyzer(match.Source, match.MatchName)
            }
            matchesFromParser1 = nil // Очищаем список после отправки
        }
        matchesMutex.Unlock()

        // Затем отправляем матчи от второго парсера
        matchesMutex.Lock()
        if len(matchesFromParser2) > 0 {
            for _, match := range matchesFromParser2 {
                forwardToAnalyzer(match.Source, match.MatchName)
            }
            matchesFromParser2 = nil // Очищаем список после отправки
        }
        matchesMutex.Unlock()
    }
}

// Пересылка данных в анализатор
func forwardToAnalyzer(source, matchName string) {
    analyzerMutex.Lock()
    defer analyzerMutex.Unlock()

    if analyzerConnection != nil {
        message := source + ": " + matchName
        log.Printf("[Matching] Отправка в анализатор: %s", message)
        if err := analyzerConnection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
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
