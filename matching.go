package main

import (
    "log"
    "sync"

    "github.com/gorilla/websocket"
)

var analyzerConnection *websocket.Conn
var analyzerMutex = sync.Mutex{}

// Запуск мэтчинга
func startMatching() {
    log.Println("[Matching] Мэтчинг запущен")
    go connectToAnalyzer()
    go connectParser("ws://localhost:7100", "Sansabet")
    go connectParser("ws://localhost:7200", "Pinnacle")
}

// Подключение к анализатору
func connectToAnalyzer() {
    for {
        log.Println("[Matching] Подключение к анализатору на ws://localhost:7400")
        conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:7400", nil)
        if err != nil {
            log.Printf("[Matching] Ошибка подключения к анализатору: %v. Повтор через 5 секунд.", err)
            continue
        }
        analyzerMutex.Lock()
        analyzerConnection = conn
        analyzerMutex.Unlock()
        log.Println("[Matching] Успешное подключение к анализатору")
        break
    }
}

// Подключение к парсерам
func connectParser(url, source string) {
    log.Printf("[Matching] Подключение к парсеру %s на %s", source, url)
    for {
        conn, _, err := websocket.DefaultDialer.Dial(url, nil)
        if err != nil {
            log.Printf("[Matching] Ошибка подключения к %s: %v. Повтор через 5 секунд.", source, err)
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
            log.Printf("[Matching] Сообщение от %s: %s", source, string(msg))
            forwardToAnalyzer(msg)
        }
    }
}

// Пересылка данных в анализатор
func forwardToAnalyzer(data []byte) {
    analyzerMutex.Lock()
    defer analyzerMutex.Unlock()

    if analyzerConnection != nil {
        log.Printf("[Matching] Отправка в анализатор: %s", string(data))
        if err := analyzerConnection.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[Matching] Ошибка отправки данных в анализатор: %v", err)
            analyzerConnection = nil
        }
    }
}

// Главная функция
func main() {
    startMatching()
    select {} // Бесконечный цикл, чтобы процесс продолжал работать
}
