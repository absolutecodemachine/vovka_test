package main

import (
	"encoding/json"
	//"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Заранее подготовленные данные для отправки
var mockData = []map[string]interface{}{
	{
		"Name":       "Roi Et United vs Buriram United",
		"Pid":        int64(4045678), // Исправлено на int64
		"Slid":       int64(0),         // Исправлено на int64
		"LeagueName": "Thailand - FA Cup",
		"MatchName":  "Roi Et United vs Buriram United",
		"MatchId":    "4045678",
		"LeagueId":   "0",
		"Win1x2": map[string]float64{
			"Win1":    3,
			"WinNone": 3,
			"Win2":    3,
		},
		"Totals": map[string]map[string]float64{
			"0.75": {"WinMore": 1.254, "WinLess": 3.26},
			"1.00": {"WinMore": 1.359, "WinLess": 2.73},
		},
		"Handicap": map[string]map[string]float64{
			"0.25": {"Win1": 3.23, "Win2": 1.251},
			"0.50": {"Win1": 2.41, "Win2": 1.448},
		},
	},
}

// Глобальные переменные
var (
	ListGames    = make(map[int64]OneGame)
	ListGamesMux sync.RWMutex
	serverInstance *Server
)

// Структуры данных
type OneGame struct {
	Name             string                 `json:"Name"`
	Pid              int64                  `json:"Pid"`
	Slid             int64                  `json:"Slid"`
	LeagueName       string                 `json:"LeagueName"`
	MatchName        string                 `json:"MatchName"`
	MatchId          string                 `json:"MatchId"`
	LeagueId         string                 `json:"LeagueId"`
	Win1x2           map[string]float64     `json:"Win1x2"`
	Totals           map[string]map[string]float64 `json:"Totals"`
	Handicap         map[string]map[string]float64 `json:"Handicap"`
}

type Server struct {
	clients       map[*websocket.Conn]bool
	handleMessage func(message []byte)
	mutex         sync.Mutex
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Функция для обработки WebSocket
func (server *Server) echo(w http.ResponseWriter, r *http.Request) {
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Ошибка обновления соединения:", err)
		return
	}
	defer connection.Close()

	server.clients[connection] = true
	defer delete(server.clients, connection)

	for {
		mt, message, err := connection.ReadMessage()
		if err != nil || mt == websocket.CloseMessage {
			break
		}

		go server.handleMessage(message)
	}
}

// Функция для отправки сообщений через WebSocket
func (server *Server) WriteMessage(message []byte) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	for conn := range server.clients {
		err := conn.WriteMessage(websocket.TextMessage, message)
		if err != nil {
			log.Printf("Ошибка отправки сообщения: %v", err)
			conn.Close()
			delete(server.clients, conn)
		}
	}
}

// Запуск WebSocket-сервера
func StartServer(handleMessage func(message []byte)) *Server {
	server := &Server{
		clients:       make(map[*websocket.Conn]bool),
		handleMessage: handleMessage,
	}

	http.HandleFunc("/", server.echo)
	go http.ListenAndServe(":7200", nil)

	return server
}

// Псевдопарсинг игр
func ParseGames() {
	ListGamesMux.Lock()
	defer ListGamesMux.Unlock()

	for _, data := range mockData {
		jsonData, err := json.Marshal(data)
		if err != nil {
			log.Println("Ошибка преобразования в JSON:", err)
			continue
		}

		serverInstance.WriteMessage(jsonData)
		log.Printf("Отправлены данные: %s\n", jsonData)
	}
}

// Псевдопарсинг событий
func ParseEvents() {
	ListGamesMux.Lock()
	defer ListGamesMux.Unlock()

	for _, data := range mockData {
		pid := data["Pid"].(int64)
		slid := data["Slid"].(int64) // Явное приведение типа
		ListGames[pid] = OneGame{
			Name:       data["Name"].(string),
			Pid:        pid,
			Slid:       slid,
			LeagueName: data["LeagueName"].(string),
			MatchName:  data["MatchName"].(string),
			MatchId:    data["MatchId"].(string),
			LeagueId:   data["LeagueId"].(string),
			Win1x2:     data["Win1x2"].(map[string]float64),
			Totals:     data["Totals"].(map[string]map[string]float64),
			Handicap:   data["Handicap"].(map[string]map[string]float64),
		}
	}

	log.Println("Обновлен список игр для псевдопарсера")
}

// Запуск событийного парсинга
func ParseEventsStart() {
	for {
		ParseEvents()
		time.Sleep(5 * time.Second)
	}
}

// Запуск парсинга игр
func ParseGamesStart() {
	for {
		ParseGames()
		time.Sleep(5 * time.Second)
	}
}

// Основная функция
func main() {
	log.Println("Запуск псевдопарсера")
	serverInstance = StartServer(func(message []byte) {
		log.Println("Сообщение от клиента:", string(message))
	})

	go ParseEventsStart()
	go ParseGamesStart()

	for {
		time.Sleep(10 * time.Second)
	}
}
