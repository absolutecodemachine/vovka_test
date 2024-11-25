package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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

// Структура для данных ставки
type BetData struct {
	Timestamp       string  `json:"timestamp"`
	League         string  `json:"league"`
	Match          string  `json:"match"`
	Outcome        string  `json:"outcome"`
	Amount         float64 `json:"amount"`
	UserCoefficient float64 `json:"userCoefficient"`
	PinnacleOdds   float64 `json:"pinnacleOdds"`
}

// Структура для логирования результатов ставок
type LogBetData struct {
	Timestamp       string  `json:"timestamp"`
	MatchName       string  `json:"matchName"`
	LeagueName      string  `json:"leagueName"`
	Outcome         string  `json:"outcome"`
	Amount          float64 `json:"amount"`
	Coefficient     float64 `json:"coefficient"`
	Status          string  `json:"status"` // "attempt", "accepted", "rejected"
}

// Глобальные переменные
var (
	calculatorData *RequestData = nil
	clients        = make(map[*websocket.Conn]bool) // Подключенные клиенты
	upgrader       = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true }, // Разрешаем все запросы
	}
	logFile = "calculator_log.txt"
)

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

// Функция для записи в лог
func writeToLog(message string) error {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(message + "\n")
	return err
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

// Обработчик для получения ставки
func betHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var betData BetData
	if err := json.NewDecoder(r.Body).Decode(&betData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Записываем попытку ставки
	attemptMsg := fmt.Sprintf("Попытка ставки: [%s] Лига: %s, Матч: %s, Исход: %s, Сумма: %.2f, Коэф. пользователя: %.2f, Коэф. Pinnacle: %.2f",
		betData.Timestamp, betData.League, betData.Match, betData.Outcome, betData.Amount, betData.UserCoefficient, betData.PinnacleOdds)
	
	if err := writeToLog(attemptMsg); err != nil {
		log.Printf("Ошибка записи в лог: %v", err)
	}

	// Фиктивный расчет прибыли (всегда возвращает 5)
	profit := 5.0

	// Записываем сделанную ставку
	betMsg := fmt.Sprintf("Сделанная ставка: [%s] Лига: %s, Матч: %s, Исход: %s, Сумма: %.2f, Коэф. пользователя: %.2f, Коэф. Pinnacle: %.2f, Прибыль: %.2f",
		betData.Timestamp, betData.League, betData.Match, betData.Outcome, betData.Amount, betData.UserCoefficient, betData.PinnacleOdds, profit)
	
	if err := writeToLog(betMsg); err != nil {
		log.Printf("Ошибка записи в лог: %v", err)
	}

	w.WriteHeader(http.StatusOK)
}

// Эндпоинт для логирования ставок
func logBetHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var betData map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&betData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	logEntry := fmt.Sprintf("[%s] Match: %v, League: %v, Outcome: %v, Type: %v",
		time.Now().Format("2006-01-02 15:04:05"),
		betData["matchName"],
		betData["leagueName"],
		betData["outcome"],
		betData["type"])

	if betData["type"] == "accepted" {
		logEntry += fmt.Sprintf(", Amount: %v, Coefficient: %v", betData["amount"], betData["coefficient"])
	}
	if pinnacleOdds, ok := betData["pinnacleOdds"]; ok {
		logEntry += fmt.Sprintf(", Pinnacle: %v", pinnacleOdds)
	}
	logEntry += "\n"

	f, err := os.OpenFile("bets.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if _, err := f.WriteString(logEntry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
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
	http.HandleFunc("/bet", enableCors(betHandler))
	http.HandleFunc("/log_bet", logBetHandler) // Добавляем новый эндпоинт
	http.HandleFunc("/ws", wsHandler) // Эндпоинт для WebSocket

	// Запуск горутины для регулярной отправки данных
	go broadcastData()

	fmt.Println("Server is running on :7500")
	log.Fatal(http.ListenAndServe(":7500", nil))
}
