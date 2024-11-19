package main

import (
    "encoding/json"
    "log"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
)

// Моковые данные
var mockData = []map[string]interface{}{
    {
        "Name":       "Roi Et United vs Buriram United",
        "Pid":        1600845644,
        "Slid":       0,
        "LeagueName": "Thailand - FA Cup",
        "MatchName":  "Roi Et United vs Buriram United",
        "MatchId":    "1600845644",
        "LeagueId":   "0",
        "Win1x2": map[string]float64{
            "Win1":    2,
            "WinNone": 2.4,
            "Win2":    3,
        },
        "Totals": map[string]map[string]float64{
            "0.75": {"WinMore": 1.254, "WinLess": 3.26},
            "1.00": {"WinMore": 1.359, "WinLess": 2.73},
            "1.25": {"WinMore": 1.662, "WinLess": 2},
            "1.50": {"WinMore": 1.961, "WinLess": 1.699},
            "1.75": {"WinMore": 2.3, "WinLess": 1.502},
            "2.00": {"WinMore": 2.98, "WinLess": 1.303},
            "2.25": {"WinMore": 3.37, "WinLess": 1.238},
            "3.00": {"WinMore": 1.306, "WinLess": 2.82},
            "3.25": {"WinMore": 1.471, "WinLess": 2.37},
            "3.50": {"WinMore": 1.628, "WinLess": 2.08},
            "3.75": {"WinMore": 1.787, "WinLess": 1.877},
            "4.00": {"WinMore": 2.03, "WinLess": 1.657},
            "4.25": {"WinMore": 2.27, "WinLess": 1.515},
            "4.50": {"WinMore": 2.46, "WinLess": 1.406},
        },
        "Handicap": map[string]map[string]float64{
            "0.25": {"Win1": 3.23, "Win2": 1.251},
            "0.50": {"Win1": 2.41, "Win2": 1.448},
            "0.75": {"Win1": 2.07, "Win2": 1.613},
            "1.00": {"Win1": 1.699, "Win2": 1.934},
            "1.25": {"Win1": 1.473, "Win2": 2.35},
            "1.50": {"Win1": 1.352, "Win2": 2.73},
            "1.75": {"Win1": 1.232, "Win2": 3.36},
            "2.00": {"Win1": 2.99, "Win2": 1.301},
            "2.25": {"Win1": 2.39, "Win2": 1.465},
            "2.50": {"Win1": 2.06, "Win2": 1.625},
            "2.75": {"Win1": 1.9, "Win2": 1.751},
            "3.00": {"Win1": 1.724, "Win2": 1.925},
            "3.25": {"Win1": 1.595, "Win2": 2.11},
            "3.50": {"Win1": 1.505, "Win2": 2.29},
        },
        "FirstTeamTotals": map[string]map[string]float64{
            "0.50": {"WinMore": 3.43, "WinLess": 1.21},
        },
        "SecondTeamTotals": map[string]map[string]float64{
            "1.50": {"WinMore": 2.33, "WinLess": 1.46},
            "3.50": {"WinMore": 1.961, "WinLess": 1.68},
        },
    },
}

// WebSocket-сервер
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true // Разрешаем все запросы
    },
}

// Обработчик WebSocket
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("Ошибка соединения:", err)
        return
    }
    defer conn.Close()

    log.Println("Клиент подключен")
    ticker := time.NewTicker(2 * time.Second) // Отправляем данные каждые 2 секунды
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            for _, data := range mockData {
                jsonData, err := json.Marshal(data)
                if err != nil {
                    log.Println("Ошибка преобразования в JSON:", err)
                    continue
                }

                if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
                    log.Println("Ошибка отправки данных:", err)
                    return
                }

                log.Printf("Отправлены данные: %s\n", jsonData)
            }
        }
    }
}

// Основная функция программы
func main() {
    http.HandleFunc("/", handleWebSocket)
    log.Println("Псевдопарсер Pinnacle запущен на порту 7100")
    log.Fatal(http.ListenAndServe(":7100", nil))
}
