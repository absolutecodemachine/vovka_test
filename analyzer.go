package main

import (
    "encoding/json"
    "log"
    "net/http"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
}

var matchPairs []MatchPair
var pairsMutex sync.Mutex

var sansabetConnection *websocket.Conn
var pinnacleConnection *websocket.Conn

var frontendClients = make(map[*websocket.Conn]bool)
var frontendMutex = sync.Mutex{}
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// Запуск анализатора
func startAnalyzer() {
    log.Println("[Analyzer] Анализатор запущен")
    go connectParsers()
    go processPairs()
    go startFrontendServer()

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения: %v", err)
            return
        }
        defer conn.Close()

        for {
            _, msg, err := conn.ReadMessage()
            log.Printf("[Analyzer] Полученные данные от мэтчинга: %s", string(msg))
            if err != nil {
                log.Printf("[Analyzer] Ошибка чтения данных: %v", err)
                break
            }

            var pairs []MatchPair
            if err := json.Unmarshal(msg, &pairs); err != nil {
                log.Printf("[Analyzer] Ошибка парсинга данных: %v", err)
                continue
            }

            pairsMutex.Lock()
            matchPairs = pairs
            pairsMutex.Unlock()
            log.Printf("[Analyzer] Обновлён список пар: %v", pairs)
        }
    })

    log.Fatal(http.ListenAndServe(":7400", nil))
}

// Подключение к парсерам
func connectParsers() {
    go connectToParser("ws://localhost:7100", &sansabetConnection, "Sansabet")
    go connectToParser("ws://localhost:7200", &pinnacleConnection, "Pinnacle")
}

func connectToParser(url string, connection **websocket.Conn, name string) {
    for {
        conn, _, err := websocket.DefaultDialer.Dial(url, nil)
        if err != nil {
            log.Printf("[Analyzer] Ошибка подключения к %s: %v", name, err)
            time.Sleep(5 * time.Second)
            continue
        }
        *connection = conn
        log.Printf("[Analyzer] Подключение к %s установлено", name)
        break
    }
}

// Обработка пар
func processPairs() {
    for {
        time.Sleep(1 * time.Second)

        pairsMutex.Lock()
        currentPairs := make([]MatchPair, len(matchPairs))
        copy(currentPairs, matchPairs)
        pairsMutex.Unlock()
        log.Printf("[Analyzer] Текущий список пар для анализа: %v", currentPairs)

        for _, pair := range currentPairs {
            processPair(pair)
        }
    }
}

// Обработка одной пары
func processPair(pair MatchPair) {
    sansabetData := fetchOdds(sansabetConnection, pair.SansabetId)
    pinnacleData := fetchOdds(pinnacleConnection, pair.PinnacleId)

    if sansabetData != "" && pinnacleData != "" {
        analyzeAndSend(pair.MatchName, sansabetData, pinnacleData)
    }
}

// Запрос данных
func fetchOdds(conn *websocket.Conn, matchId string) string {
    if conn == nil {
        return ""
    }

    if err := conn.WriteMessage(websocket.TextMessage, []byte(matchId)); err != nil {
        log.Printf("[Analyzer] Ошибка отправки ID: %v", err)
        return ""
    }

    _, msg, err := conn.ReadMessage()
    if err != nil {
        log.Printf("[Analyzer] Ошибка чтения данных: %v", err)
        return ""
    }

    return string(msg)
}

// Анализ данных и отправка на сайт
func analyzeAndSend(matchName, sansabetData, pinnacleData string) {
    message := matchName + " | Sansabet: " + sansabetData + " | Pinnacle: " + pinnacleData
    log.Printf("[Analyzer] Отправка данных на сайт: %s", message)
    forwardToFrontend([]byte(message))
}

// Отправка на сайт
func forwardToFrontend(data []byte) {
    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            client.Close()
            delete(frontendClients, client)
        }
    }
}

// Запуск фронтенд-сервера
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

    log.Fatal(http.ListenAndServe(":7300", nil))
}

// Главная функция
func main() {
    startAnalyzer()
    select {}
}
