package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

// Структура для исходов
type Outcome struct {
	Outcome   string  `json:"Outcome"`
	Pinnacle  float64 `json:"Pinnacle"`
	ROI       float64 `json:"ROI"`
	Sansabet  float64 `json:"Sansabet"`
}

// Структура для данных матча
type RequestData struct {
	Country         string    `json:"Country"`
	LeagueName      string    `json:"LeagueName"`
	MatchName       string    `json:"MatchName"`
	Outcomes        []Outcome `json:"Outcomes"`
	PinnacleId      string    `json:"PinnacleId"`
	SansabetId      string    `json:"SansabetId"`
	SelectedOutcome Outcome   `json:"SelectedOutcome"` // Добавлено поле SelectedOutcome
}

// Глобальные переменные
var calculatorData *RequestData = nil
var clients = make(map[*websocket.Conn]bool) // Подключенные клиенты
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // Разрешаем все запросы
}

// Middleware для добавления CORS-заголовков
func enableCors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*") // Разрешить все источники
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// Эндпоинт для получения данных от фронтенда
func receiveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	decoder := json.NewDecoder(r.Body)
	defer r.Body.Close()

	var data RequestData
	if err := decoder.Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		log.Printf("Ошибка декодирования JSON: %v", err)
		return
	}

	calculatorData = &data // Сохраняем данные
	log.Printf("Получены данные: %+v", calculatorData)

	w.WriteHeader(http.StatusOK)
}

// Функция для отправки данных всем клиентам
func broadcastData() {
	for {
		time.Sleep(2 * time.Second) // Отправляем данные раз в 2 секунды

		if calculatorData == nil {
			continue // Если данных нет, ничего не отправляем
		}

		data, err := json.Marshal(calculatorData)
		if err != nil {
			log.Printf("Ошибка сериализации данных: %v", err)
			continue
		}

		for client := range clients {
			err := client.WriteMessage(websocket.TextMessage, data)
			if err != nil {
				log.Printf("Ошибка отправки данных клиенту: %v", err)
				client.Close()
				delete(clients, client) // Удаляем клиента из списка
			}
		}
	}
}

// Эндпоинт для подключения WebSocket
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Ошибка подключения WebSocket: %v", err)
		return
	}

	clients[conn] = true
	log.Println("Новый клиент подключён")

	// Поддерживаем соединение
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Ошибка чтения сообщения: %v", err)
			conn.Close()
			delete(clients, conn)
			break
		}
	}
}

func main() {
	http.HandleFunc("/receive", enableCors(receiveHandler)) // Эндпоинт для получения данных с поддержкой CORS
	http.HandleFunc("/ws", wsHandler)                       // Эндпоинт для WebSocket

	// Запуск горутины для регулярной отправки данных
	go broadcastData()

	port := ":7500"
	fmt.Printf("Calculator server running on port %s\n", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
