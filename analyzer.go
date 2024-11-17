package main

import (
    "log"
    "net/http"
    "sync"

    "github.com/gorilla/websocket"
)

var frontendClients = make(map[*websocket.Conn]bool)
var frontendMutex = sync.Mutex{}
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// Запуск анализатора
func startAnalyzer() {
    log.Println("[Analyzer] Анализатор запущен")
    go startFrontendServer()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { // Принимаем соединения от мэтчинга
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения к анализатору: %v", err)
            return
        }
        log.Println("[Analyzer] Подключение к анализатору установлено")
        defer conn.Close()

        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                log.Printf("[Analyzer] Ошибка чтения данных в анализаторе: %v", err)
                break
            }
            log.Printf("[Analyzer] Получено сообщение: %s", string(msg))
            forwardToFrontend(msg)
        }
    })

    log.Println("[Analyzer] Запуск анализатора на порту 7400")
    log.Fatal(http.ListenAndServe(":7400", nil))
}

// Пересылка данных на фронтенд
func forwardToFrontend(data []byte) {
    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    log.Printf("[Analyzer] Отправка данных на фронтенд: %s", string(data))
    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[Analyzer] Ошибка отправки данных клиенту: %v", err)
            client.Close()
            delete(frontendClients, client)
        }
    }
}

// Запуск веб-сервера для фронтенда
func startFrontendServer() {
    http.HandleFunc("/output", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения клиента: %v", err)
            return
        }

        frontendMutex.Lock()
        frontendClients[conn] = true
        frontendMutex.Unlock()

        log.Println("[Analyzer] Клиент подключён")
        defer func() {
            frontendMutex.Lock()
            delete(frontendClients, conn)
            frontendMutex.Unlock()
            log.Println("[Analyzer] Клиент отключён")
        }()

        for {
            if _, _, err := conn.NextReader(); err != nil {
                break
            }
        }
    })

    log.Println("[Analyzer] Запуск фронтенд-сервера на порту 7300")
    log.Fatal(http.ListenAndServe(":7300", nil))
}

// Главная функция
func main() {
    startAnalyzer()
    select {} // Бесконечный цикл, чтобы процесс продолжал работать
}
