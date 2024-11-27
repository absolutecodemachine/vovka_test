package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Константы для Pinnacle API
const (
	PINNACLE_API_URL = "https://api.pinnacle.com/"
	SPORT_ID         = 29
	LIVE_MODE        = 1
	ODDS_FORMAT      = "Decimal"
	REQUEST_TIMEOUT  = 1 * time.Second

	// Данные для подключения к Pinnacle
	PINNACLE_USERNAME = "AG1677099"
	PINNACLE_PASSWORD = "5421123A"
	PROXY             = "http://AllivanService:PinnacleProxy@154.7.188.227:5242"
)

// Структура для исходов
type Outcome struct {
	Outcome  string  `json:"Outcome"`
	Pinnacle float64 `json:"Pinnacle"`
	ROI      float64 `json:"ROI"`
	Sansabet float64 `json:"Sansabet"`
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
	MatchId     string  `json:"matchId"`
	Amount      float64 `json:"amount"`
	Coefficient float64 `json:"coefficient"`
}

// Структура для логирования результатов ставок
type LogBetData struct {
	Timestamp   string  `json:"timestamp"`
	MatchName   string  `json:"matchName"`
	LeagueName  string  `json:"leagueName"`
	Outcome     string  `json:"outcome"`
	Amount      float64 `json:"amount"`
	Coefficient float64 `json:"coefficient"`
	Status      string  `json:"status"` // "attempt", "accepted", "rejected"
}

// MatchBetHistory хранит информацию о ставках на конкретный матч
type MatchBetHistory struct {
	TotalBetAmount float64   // Общая сумма сделанных ставок
	LastUpdateTime time.Time // Время последнего обновления
}

// BetHistoryStorage хранит историю ставок
type BetHistoryStorage struct {
	Matches map[string]*MatchBetHistory // ключ: ID матча
	mutex   sync.RWMutex
}

// PinnacleAPI для взаимодействия с API
type PinnacleAPI struct {
	Username string
	Password string
	Client   *http.Client
}

// Status структура для отслеживания статуса API
type PinnacleStatus struct {
	IsAvailable bool      `json:"isAvailable"`
	LastUpdate  time.Time `json:"lastUpdate"`
	Error       string    `json:"error"`
}

var (
	calculatorData *RequestData = nil
	clients                     = make(map[*websocket.Conn]bool) // Подключенные клиенты
	upgrader                    = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true }, // Разрешаем все запросы
	}
	logFile                     = "calculator_log.txt"
	pinnacleAPI    *PinnacleAPI // Для работы с Pinnacle API
	pinnacleStatus = PinnacleStatus{
		IsAvailable: true,
		LastUpdate:  time.Now(),
	}
	betHistory = &BetHistoryStorage{
		Matches: make(map[string]*MatchBetHistory),
	}
	betHistoryFile = "bet_history.json"
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

// CalculateBetRequest структура для запроса расчета ставки
type CalculateBetRequest struct {
	MatchID string  `json:"matchId"`
	Odds    float64 `json:"odds"`
	Edge    float64 `json:"edge"`
	Risk    float64 `json:"risk"`
	Bank    float64 `json:"bank"`
}

// CalculateBetResponse структура для ответа с расчетом ставки
type CalculateBetResponse struct {
	OriginalAmount float64 `json:"originalAmount"`
	AdjustedAmount float64 `json:"adjustedAmount"`
	Percentage     float64 `json:"percentage"`
}

// calculateBetHandler обработчик для расчета размера ставки
func calculateBetHandler(w http.ResponseWriter, r *http.Request) {
    log.Println("1. calculateBetHandler: запрос получен")

    if r.Method != http.MethodPost {
        log.Printf("2. Ошибка: неверный метод %s\n", r.Method)
        http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
        return
    }

    // Читаем тело запроса для логирования
    body, err := io.ReadAll(r.Body)
    if err != nil {
        log.Printf("3. Ошибка чтения тела запроса: %v\n", err)
        http.Error(w, "Error reading request body", http.StatusBadRequest)
        return
    }
    log.Printf("4. Тело запроса: %s\n", string(body))

    // Создаем новый reader из сохраненного тела
    r.Body = io.NopCloser(bytes.NewBuffer(body))

    var req CalculateBetRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Printf("5. Ошибка декодирования JSON: %v\n", err)
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }

    log.Printf("6. Параметры запроса: odds=%.2f edge=%.2f risk=%.2f bank=%.2f\n",
        req.Odds, req.Edge, req.Risk, req.Bank)

    // Рассчитываем размер ставки
    originalAmount := getBetSize(req.Odds, req.Edge, req.Risk, req.Bank)
    log.Printf("7. Рассчитан originalAmount: %.2f\n", originalAmount)

    adjustedAmount := calculateAdjustedBetSize(req.MatchID, req.Odds, req.Edge, req.Risk, req.Bank)
    log.Printf("8. Рассчитан adjustedAmount: %.2f\n", adjustedAmount)

    // Рассчитываем процент оставшейся суммы
    percentage := 100.0
    if originalAmount > 0 {
        percentage = (adjustedAmount / originalAmount) * 100
        if percentage < 0 {
            percentage = 0
        } else if percentage > 100 {
            percentage = 100
        }
    } else {
        percentage = 0
    }
    log.Printf("9. Рассчитан percentage: %.2f%%\n", percentage)

    response := CalculateBetResponse{
        OriginalAmount: originalAmount,
        AdjustedAmount: adjustedAmount,
        Percentage:     percentage,
    }

    log.Printf("10. Подготовлен ответ: %+v\n", response)

    w.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w).Encode(response); err != nil {
        log.Printf("11. Ошибка кодирования ответа: %v\n", err)
        http.Error(w, "Error encoding response", http.StatusInternalServerError)
        return
    }
    log.Println("12. Ответ успешно отправлен")
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

	// Обновляем историю ставок, используя matchId напрямую из запроса
	betHistory.updateMatchBet(betData.MatchId, betData.Amount)

	// Сохраняем историю ставок
	if err := betHistory.saveBetHistory(); err != nil {
		log.Printf("Ошибка сохранения истории ставок: %v", err)
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

// Создание нового экземпляра PinnacleAPI
func NewPinnacleAPI(username, password, proxy string) *PinnacleAPI {
	transport := &http.Transport{}

	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			log.Printf("[ERROR] Неверный URL прокси: %v\n", err)
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   REQUEST_TIMEOUT,
	}

	return &PinnacleAPI{
		Username: username,
		Password: password,
		Client:   client,
	}
}

// Построение URL для запроса к API
func (api *PinnacleAPI) buildURL(endpoint string, params map[string]string) string {
	baseURL := PINNACLE_API_URL + endpoint
	if len(params) == 0 {
		return baseURL
	}

	values := url.Values{}
	for key, value := range params {
		values.Add(key, value)
	}
	return baseURL + "?" + values.Encode()
}

// Выполнение запроса к API
func (api *PinnacleAPI) query(urlStr string) (map[string]interface{}, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(api.Username, api.Password)
	resp, err := api.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// GetMatchOdds получает коэффициенты для конкретного матча
func (api *PinnacleAPI) GetMatchOdds(matchId string) (map[string]float64, bool, error) {
	// Построение URL для запроса odds
	params := map[string]string{
		"matchId": matchId,
	}
	urlStr := api.buildURL("/v1/odds", params)

	// Выполнение запроса
	result, err := api.query(urlStr)
	if err != nil {
		pinnacleStatus.IsAvailable = false
		pinnacleStatus.Error = err.Error()
		pinnacleStatus.LastUpdate = time.Now()
		return nil, false, fmt.Errorf("failed to fetch odds: %v", err)
	}

	// Парсинг и обработка результата
	odds := make(map[string]float64)
	if leagues, ok := result["leagues"].([]interface{}); ok && len(leagues) > 0 {
		if league, ok := leagues[0].(map[string]interface{}); ok {
			if events, ok := league["events"].([]interface{}); ok && len(events) > 0 {
				if event, ok := events[0].(map[string]interface{}); ok {
					// Проверяем статус
					status, ok := event["status"].(string)
					if !ok || status != "OPEN" {
						pinnacleStatus.IsAvailable = false
						pinnacleStatus.Error = "Match is not open"
						pinnacleStatus.LastUpdate = time.Now()
						return nil, false, nil
					}

					if periods, ok := event["periods"].([]interface{}); ok && len(periods) > 0 {
						if period, ok := periods[0].(map[string]interface{}); ok {
							// Обработка 1X2
							if moneyline, ok := period["moneyline"].(map[string]interface{}); ok {
								if home, ok := moneyline["home"].(float64); ok {
									odds["Win1"] = home
								}
								if draw, ok := moneyline["draw"].(float64); ok {
									odds["WinNone"] = draw
								}
								if away, ok := moneyline["away"].(float64); ok {
									odds["Win2"] = away
								}
							}

							// Обработка тоталов
							if totals, ok := period["totals"].([]interface{}); ok {
								for _, total := range totals {
									if t, ok := total.(map[string]interface{}); ok {
										if points, ok := t["points"].(float64); ok {
											key := fmt.Sprintf("Total%.1f", points)
											if over, ok := t["over"].(float64); ok {
												odds[key+"More"] = over
											}
											if under, ok := t["under"].(float64); ok {
												odds[key+"Less"] = under
											}
										}
									}
								}
							}

							// Обработка гандикапов
							if spreads, ok := period["spreads"].([]interface{}); ok {
								for _, spread := range spreads {
									if s, ok := spread.(map[string]interface{}); ok {
										if hdp, ok := s["hdp"].(float64); ok {
											key := fmt.Sprintf("Handicap%.1f", hdp)
											if home, ok := s["home"].(float64); ok {
												odds[key+"1"] = home
											}
											if away, ok := s["away"].(float64); ok {
												odds[key+"2"] = away
											}
										}
									}
								}
							}

							// Обработка индивидуальных тоталов
							if teamTotals, ok := period["teamTotal"].(map[string]interface{}); ok {
								// Тоталы первой команды
								if home, ok := teamTotals["home"].([]interface{}); ok {
									for _, total := range home {
										if t, ok := total.(map[string]interface{}); ok {
											if points, ok := t["points"].(float64); ok {
												key := fmt.Sprintf("FirstTeamTotal%.1f", points)
												if over, ok := t["over"].(float64); ok {
													odds[key+"More"] = over
												}
												if under, ok := t["under"].(float64); ok {
													odds[key+"Less"] = under
												}
											}
										}
									}
								}
								// Тоталы второй команды
								if away, ok := teamTotals["away"].([]interface{}); ok {
									for _, total := range away {
										if t, ok := total.(map[string]interface{}); ok {
											if points, ok := t["points"].(float64); ok {
												key := fmt.Sprintf("SecondTeamTotal%.1f", points)
												if over, ok := t["over"].(float64); ok {
													odds[key+"More"] = over
												}
												if under, ok := t["under"].(float64); ok {
													odds[key+"Less"] = under
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Проверяем, что получили хотя бы один коэффициент
	if len(odds) == 0 {
		pinnacleStatus.IsAvailable = false
		pinnacleStatus.Error = "No odds available"
		pinnacleStatus.LastUpdate = time.Now()
		return nil, false, nil
	}

	// Обновляем статус успешного получения данных
	pinnacleStatus.IsAvailable = true
	pinnacleStatus.Error = ""
	pinnacleStatus.LastUpdate = time.Now()

	return odds, true, nil
}

// UpdateMatchOdds обновляет коэффициенты для текущего матча
func updateMatchOdds() {
	if calculatorData != nil && calculatorData.PinnacleId != "" {
		odds, available, err := pinnacleAPI.GetMatchOdds(calculatorData.PinnacleId)
		if err != nil {
			fmt.Printf("Error updating odds: %v\n", err)
			return
		}

		if !available {
			// Если коэффициенты недоступны, очищаем их
			for i := range calculatorData.Outcomes {
				calculatorData.Outcomes[i].Pinnacle = 0
			}
		} else {
			// Обновляем коэффициенты для всех исходов
			for i, outcome := range calculatorData.Outcomes {
				if pinnacleOdd, ok := odds[outcome.Outcome]; ok {
					calculatorData.Outcomes[i].Pinnacle = pinnacleOdd
				} else {
					calculatorData.Outcomes[i].Pinnacle = 0
				}
			}
		}

		// Отправляем обновленные данные клиентам
		broadcastData()
	}
}

// Добавляем новый эндпоинт для получения статуса
func pinnacleStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pinnacleStatus)
}

// saveBetHistory сохраняет историю ставок в файл
func (s *BetHistoryStorage) saveBetHistory() error {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	data, err := json.Marshal(s.Matches)
	if err != nil {
		return fmt.Errorf("ошибка маршалинга истории ставок: %v", err)
	}

	return os.WriteFile(betHistoryFile, data, 0644)
}

// loadBetHistory загружает историю ставок из файла
func (s *BetHistoryStorage) loadBetHistory() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	data, err := os.ReadFile(betHistoryFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Файл не существует, это нормально при первом запуске
		}
		return fmt.Errorf("ошибка чтения файла истории ставок: %v", err)
	}

	return json.Unmarshal(data, &s.Matches)
}

// cleanOldRecords удаляет записи старше 48 часов
func (s *BetHistoryStorage) cleanOldRecords() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	threshold := time.Now().Add(-48 * time.Hour)
	for matchID, history := range s.Matches {
		if history.LastUpdateTime.Before(threshold) {
			delete(s.Matches, matchID)
		}
	}
}

// updateMatchBet обновляет информацию о ставке на матч
func (s *BetHistoryStorage) updateMatchBet(matchID string, amount float64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, exists := s.Matches[matchID]; !exists {
		s.Matches[matchID] = &MatchBetHistory{
			TotalBetAmount: 0,
			LastUpdateTime: time.Now(),
		}
	}

	s.Matches[matchID].TotalBetAmount += amount
	s.Matches[matchID].LastUpdateTime = time.Now()
}

// getRemainingBetPercentage возвращает оставшийся процент для ставки
func (s *BetHistoryStorage) getRemainingBetPercentage(matchID string) float64 {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if history, exists := s.Matches[matchID]; exists {
		if history.TotalBetAmount >= 100 { // Если уже поставили 100% или больше
			return 0
		}
		return 1 - (history.TotalBetAmount / 100)
	}
	return 1 // Если нет истории ставок, возвращаем 100%
}

// Добавьте эту функцию в структуру BetHistoryStorage
func (s *BetHistoryStorage) getTotalBetAmount(matchID string) float64 {
    s.mutex.RLock()
    defer s.mutex.RUnlock()

    if history, exists := s.Matches[matchID]; exists {
        return history.TotalBetAmount
    }
    return 0
}

// getBetSize рассчитывает оптимальный размер ставки на основе критерия Келли
func getBetSize(odds float64, edge float64, risk float64, bank float64) float64 {
	log.Printf("13. getBetSize начало расчета: odds=%.2f edge=%.2f risk=%.2f bank=%.2f\n",
		odds, edge, risk, bank)

	if edge < 0 {
		log.Println("14. edge < 0, возвращаем 0")
		return 0
	}

	// Преобразуем edge из процентов в десятичную дробь
	edgeDecimal := edge / 100
	log.Printf("15. edgeDecimal=%.4f\n", edgeDecimal)

	// Рассчитываем фактор внутри логарифма
	logFactor := 1 - (1 / (odds / (1 + edgeDecimal)))
	log.Printf("16. logFactor=%.4f\n", logFactor)

	// Рассчитываем процент от банкролла для ставки
	betSizePercent := math.Log10(logFactor) / math.Log10(math.Pow(10, -risk))
	log.Printf("17. betSizePercent=%.4f\n", betSizePercent)

	// Проверяем, что результат имеет смысл
	if betSizePercent < 0 || betSizePercent > 1 {
		log.Printf("18. betSizePercent=%.4f вне диапазона [0,1], возвращаем 0\n", betSizePercent)
		return 0
	}

	// Рассчитываем фактический размер ставки
	betSize := betSizePercent * bank
	log.Printf("19. betSize=%.2f\n", betSize)

	// Округляем до ближайшего числа, кратного 5
	roundedBetSize := math.Round(betSize/5) * 5
	log.Printf("20. roundedBetSize=%.2f\n", roundedBetSize)

	return roundedBetSize
}

func calculateAdjustedBetSize(matchID string, odds float64, edge float64, risk float64, bank float64) float64 {
	// Получаем базовый размер ставки
	baseBetSize := getBetSize(odds, edge, risk, bank)
	log.Printf("Базовый размер ставки (baseBetSize): %.2f\n", baseBetSize)

	// Получаем процент уже поставленных денег на матч
	totalBetAmount := betHistory.getTotalBetAmount(matchID)
	log.Printf("Процент уже поставленных ставок (totalBetAmount): %.2f%%\n", totalBetAmount)

	// Вычисляем оставшийся процент от базовой суммы ставки
	remainingPercentage := 100.0
	if baseBetSize > 0 {
		remainingPercentage -= totalBetAmount
		if remainingPercentage < 0 {
			remainingPercentage = 0
		} else if remainingPercentage > 100.0 {
			remainingPercentage = 100.0
		}
	}
	log.Printf("Оставшийся процент (remainingPercentage): %.2f%%\n", remainingPercentage)

	// Корректируем размер ставки
	adjustedBetSize := baseBetSize * remainingPercentage / 100.0
	log.Printf("Скорректированный размер ставки (adjustedBetSize): %.2f\n", adjustedBetSize)

	// Округляем до ближайшего числа, кратного 5
	adjustedBetSize = math.Round(adjustedBetSize/5) * 5
	log.Printf("Округленный скорректированный размер ставки: %.2f\n", adjustedBetSize)

	return adjustedBetSize
}



func main() {
	// Настраиваем логирование
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	fmt.Println("Запуск сервера...")

	// Инициализируем историю ставок
	betHistory = &BetHistoryStorage{
		Matches: make(map[string]*MatchBetHistory),
	}
	betHistory.loadBetHistory()

	// Запускаем очистку старых записей каждый час
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			betHistory.cleanOldRecords()
		}
	}()

	// Создаем экземпляр Pinnacle API
	pinnacleAPI = NewPinnacleAPI(PINNACLE_USERNAME, PINNACLE_PASSWORD, PROXY)

	// Настраиваем маршруты
	http.HandleFunc("/ws", wsHandler)
	http.HandleFunc("/receive", enableCors(receiveHandler))
	http.HandleFunc("/calculate_bet", enableCors(calculateBetHandler))
	http.HandleFunc("/bet", enableCors(betHandler))
	http.HandleFunc("/log_bet", enableCors(logBetHandler))
	http.HandleFunc("/pinnacle_status", enableCors(pinnacleStatusHandler))

	// Запускаем сервер
	fmt.Println("Сервер запущен на порту 7500")
	if err := http.ListenAndServe(":7500", nil); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
