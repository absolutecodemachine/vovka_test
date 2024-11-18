package main

import (
    "encoding/json"
    "log"
    "net/http"
    "sync"
    "sort"
    "time"
    "fmt"
    "github.com/gorilla/websocket"
)

type MatchPair struct {
    SansabetId string `json:"SansabetId"`
    PinnacleId string `json:"PinnacleId"`
    MatchName  string `json:"MatchName"`
}

type OddsData struct {
    Outcomes map[string]float64 `json:"outcomes"` // Пример: {"TotalOver2.5": 1.9, "TotalUnder2.5": 1.8}
}

const MARGIN = 1.08

var extraPercents = []struct {
    Min, Max, ExtraPercent float64
}{
    {2.29, 2.75, 1.03},
    {2.75, 3.2, 1.04},
    {3.2, 3.7, 1.05},
}

// Функция для нахождения extra_percent
func getExtraPercent(pinnacleOdd float64) float64 {
    for _, ep := range extraPercents {
        if pinnacleOdd >= ep.Min && pinnacleOdd < ep.Max {
            return ep.ExtraPercent
        }
    }
    return 1.0 // По умолчанию без extra_percent
}

// Функция для расчёта ROI
func calculateROI(sansaOdd, pinnacleOdd float64) float64 {
    extraPercent := getExtraPercent(pinnacleOdd)
    roi := (sansaOdd / (pinnacleOdd * MARGIN * extraPercent) - 1 - 0.03) * 100 * 0.67
    return roi
}

// Функция для нахождения пересечений
func findCommonOutcomes(sansabetData, pinnacleData string) map[string][2]float64 {
    log.Printf("[Analyzer] Входные данные Sansabet: %s", sansabetData)
    log.Printf("[Analyzer] Входные данные Pinnacle: %s", pinnacleData)

    var sansabetOdds, pinnacleOdds struct {
        Win1x2           map[string]float64 `json:"Win1x2"`
        Totals           map[string]struct {
            WinMore float64 `json:"WinMore"`
            WinLess float64 `json:"WinLess"`
        } `json:"Totals"`
        Handicap         map[string]struct {
            Win1 float64 `json:"Win1"`
            Win2 float64 `json:"Win2"`
        } `json:"Handicap"`
        FirstTeamTotals  map[string]struct {
            WinMore float64 `json:"WinMore"`
            WinLess float64 `json:"WinLess"`
        } `json:"FirstTeamTotals"`
        SecondTeamTotals map[string]struct {
            WinMore float64 `json:"WinMore"`
            WinLess float64 `json:"WinLess"`
        } `json:"SecondTeamTotals"`
    }


    common := make(map[string][2]float64)

    // Парсим данные
    if err := json.Unmarshal([]byte(sansabetData), &sansabetOdds); err != nil {
        log.Printf("[Analyzer] Ошибка парсинга данных Sansabet: %v", err)
        return common
    }
    log.Printf("[Analyzer] Распарсенные данные Sansabet: %+v", sansabetOdds)

    if err := json.Unmarshal([]byte(pinnacleData), &pinnacleOdds); err != nil {
        log.Printf("[Analyzer] Ошибка парсинга данных Pinnacle: %v", err)
        return common
    }
    log.Printf("[Analyzer] Распарсенные данные Pinnacle: %+v", pinnacleOdds)

    // Сравниваем Win1x2
    for key, sansabetValue := range sansabetOdds.Win1x2 {
        if pinnacleValue, exists := pinnacleOdds.Win1x2[key]; exists {
            if !isPinnacleOddInRange(pinnacleValue) {
                log.Printf("[Analyzer] Пропуск исхода (Win1x2): %s, Pinnacle=%f (вне диапазона)", key, pinnacleValue)
                continue
            }
            common[key] = [2]float64{sansabetValue, pinnacleValue}
            log.Printf("[Analyzer] Общий исход (Win1x2): %s, Sansabet=%f, Pinnacle=%f", key, sansabetValue, pinnacleValue)
        }
    }

    // Сравниваем Totals
    for key, sansabetTotal := range sansabetOdds.Totals {
        if pinnacleTotal, exists := pinnacleOdds.Totals[key]; exists {
            if !isPinnacleOddInRange(pinnacleTotal.WinMore) {
                log.Printf("[Analyzer] Пропуск исхода (Totals): %s, Pinnacle=%f (вне диапазона)", key, pinnacleTotal.WinMore)
                continue
            }
            common["Total "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
            log.Printf("[Analyzer] Общий исход (Totals): %s, Sansabet=%f, Pinnacle=%f", key, sansabetTotal.WinMore, pinnacleTotal.WinMore)
        }
    }

    // Сравниваем Handicap
    //log.Println("[Analyzer] Начинаем обработку Handicap")
    //log.Printf("[Analyzer] Данные Handicap Sansabet: %+v", sansabetOdds.Handicap)
    //log.Printf("[Analyzer] Данные Handicap Pinnacle: %+v", pinnacleOdds.Handicap)
    for key, sansabetHandicap := range sansabetOdds.Handicap {
        if pinnacleHandicap, exists := pinnacleOdds.Handicap[key]; exists {
            if !isPinnacleOddInRange(pinnacleHandicap.Win1) {
                log.Printf("[Analyzer] Пропуск исхода (Handicap): %s, Pinnacle=%f (вне диапазона)", key, pinnacleHandicap.Win1)
                continue
            }
            common["Handicap "+key] = [2]float64{sansabetHandicap.Win1, pinnacleHandicap.Win1}
            log.Printf("[Analyzer] Общий исход (Handicap): %s, Sansabet=%f, Pinnacle=%f", key, sansabetHandicap.Win1, pinnacleHandicap.Win1)
        }
    }

    // Сравниваем FirstTeamTotals
    if sansabetOdds.FirstTeamTotals != nil && pinnacleOdds.FirstTeamTotals != nil {
        log.Println("[Analyzer] Начинаем обработку FirstTeamTotals")
        log.Printf("[Analyzer] Данные FirstTeamTotals Sansabet: %+v", sansabetOdds.FirstTeamTotals)
        log.Printf("[Analyzer] Данные FirstTeamTotals Pinnacle: %+v", pinnacleOdds.FirstTeamTotals)

        for key, sansabetTotal := range sansabetOdds.FirstTeamTotals {
            if pinnacleTotal, exists := pinnacleOdds.FirstTeamTotals[key]; exists {
                if !isPinnacleOddInRange(pinnacleTotal.WinMore) {
                    log.Printf("[Analyzer] Пропуск исхода (FirstTeamTotals): %s, Pinnacle=%f (вне диапазона)", key, pinnacleTotal.WinMore)
                    continue
                }
                common["FirstTeamTotal "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
                log.Printf("[Analyzer] Общий исход (FirstTeamTotals): %s, Sansabet=%f, Pinnacle=%f", key, sansabetTotal.WinMore, pinnacleTotal.WinMore)
            }
        }
    } else {
        log.Println("[Analyzer] FirstTeamTotals отсутствуют в данных")
    }

    // Сравниваем SecondTeamTotals
    if sansabetOdds.SecondTeamTotals != nil && pinnacleOdds.SecondTeamTotals != nil {
        log.Println("[Analyzer] Начинаем обработку SecondTeamTotals")
        log.Printf("[Analyzer] Данные SecondTeamTotals Sansabet: %+v", sansabetOdds.SecondTeamTotals)
        log.Printf("[Analyzer] Данные SecondTeamTotals Pinnacle: %+v", pinnacleOdds.SecondTeamTotals)

        for key, sansabetTotal := range sansabetOdds.SecondTeamTotals {
            if pinnacleTotal, exists := pinnacleOdds.SecondTeamTotals[key]; exists {
                if !isPinnacleOddInRange(pinnacleTotal.WinMore) {
                    log.Printf("[Analyzer] Пропуск исхода (SecondTeamTotals): %s, Pinnacle=%f (вне диапазона)", key, pinnacleTotal.WinMore)
                    continue
                }
                common["SecondTeamTotal "+key] = [2]float64{sansabetTotal.WinMore, pinnacleTotal.WinMore}
                log.Printf("[Analyzer] Общий исход (SecondTeamTotals): %s, Sansabet=%f, Pinnacle=%f", key, sansabetTotal.WinMore, pinnacleTotal.WinMore)
            }
        }
    } else {
        log.Println("[Analyzer] SecondTeamTotals отсутствуют в данных")
    }


    log.Printf("[Analyzer] Общие исходы: %+v", common)
    return common
}

func calculateAndFilterCommonOutcomes(matchName string, commonOutcomes map[string][2]float64) []string {
    filtered := []struct {
        Outcome string
        ROI     float64
        Sansa   float64
        Pinnacle float64
    }{}

    for outcome, values := range commonOutcomes {
        sansabetOdd := values[0]
        pinnacleOdd := values[1]
        roi := calculateROI(sansabetOdd, pinnacleOdd)

        if roi > -30 { // Фильтруем только положительный ROI
            filtered = append(filtered, struct {
                Outcome  string
                ROI      float64
                Sansa    float64
                Pinnacle float64
            }{
                Outcome:  outcome,
                ROI:      roi,
                Sansa:    sansabetOdd,
                Pinnacle: pinnacleOdd,
            })
            log.Printf("[Analyzer] ROI для исхода %s: %.2f (Sansabet: %.2f, Pinnacle: %.2f)", outcome, roi, sansabetOdd, pinnacleOdd)
        } else {
            log.Printf("[Analyzer] ROI слишком низкий для исхода %s: %.2f", outcome, roi)
        }
    }

    // Сортируем по ROI в порядке убывания
    sort.Slice(filtered, func(i, j int) bool {
        return filtered[i].ROI > filtered[j].ROI
    })

    // Формируем данные для отправки
    results := []string{}
    for _, item := range filtered {
        results = append(results, fmt.Sprintf(
            "%s | Исход: %s | ROI: %.2f%% | Sansabet: %.2f | Pinnacle: %.2f",
            matchName, item.Outcome, item.ROI, item.Sansa, item.Pinnacle,
        ))
    }

    return results
}


func isPinnacleOddInRange(odd float64) bool {
    return odd >= 1.2 && odd <= 3.5
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
            //log.Printf("[Analyzer] Полученные данные от мэтчинга: %s", string(msg))
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
            //log.Printf("[Analyzer] Обновлён список пар: %v", pairs)
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
        //log.Printf("[Analyzer] Проверка текущих пар: %v", currentPairs)

        for _, pair := range currentPairs {
            processPair(pair)
        }
    }
}

// Обработка одной пары
func processPair(pair MatchPair) {
    sansabetData := fetchOdds(sansabetConnection, pair.SansabetId)
    pinnacleData := fetchOdds(pinnacleConnection, pair.PinnacleId)
    //log.Printf("[Analyzer] Данные для анализа: Sansabet=%s, Pinnacle=%s", sansabetData, pinnacleData)
    if sansabetData != "" && pinnacleData != "" {
        analyzeAndSend(pair.MatchName, sansabetData, pinnacleData)
    }
}

// Запрос данных
func fetchOdds(conn *websocket.Conn, matchId string) string {
    if conn == nil {
        log.Printf("[Analyzer] fetchOdds: conn == nil для MatchId=%s", matchId)
        return ""
    }

    log.Printf("[Analyzer] fetchOdds: Отправка ID: %s", matchId)
    if err := conn.WriteMessage(websocket.TextMessage, []byte(matchId)); err != nil {
        log.Printf("[Analyzer] fetchOdds: Ошибка отправки ID: %v", err)
        return ""
    }

    _, msg, err := conn.ReadMessage()
    if err != nil {
        log.Printf("[Analyzer] fetchOdds: Ошибка чтения данных: %v", err)
        return ""
    }

    log.Printf("[Analyzer] fetchOdds: Исходные данные от парсера (MatchId=%s): %s", matchId, string(msg))
    return string(msg)
}


// Анализ данных и отправка на сайт
func analyzeAndSend(matchName, sansabetData, pinnacleData string) {
    log.Printf("[Analyzer] Начало анализа для матча: %s", matchName)
    commonOutcomes := findCommonOutcomes(sansabetData, pinnacleData)

    if len(commonOutcomes) == 0 {
        log.Printf("[Analyzer] Общих исходов не найдено для матча: %s", matchName)
        return
    }

    // Используем новую функцию для расчёта ROI и фильтрации
    filteredOutcomes := calculateAndFilterCommonOutcomes(matchName, commonOutcomes)

    if len(filteredOutcomes) == 0 {
        log.Printf("[Analyzer] Нет исходов с положительным ROI для матча: %s", matchName)
        return
    }

    for _, message := range filteredOutcomes {
        log.Printf("[Analyzer] Отправка данных на сайт: %s", message)
        forwardToFrontend([]byte(message))
    }
}


// Вспомогательная функция для форматирования float
func formatFloat(value float64) string {
    return fmt.Sprintf("%.2f", value)
}


// Отправка на сайт
func forwardToFrontend(data []byte) {
    log.Printf("[Analyzer] Данные для отправки клиентам: %s", string(data))

    frontendMutex.Lock()
    defer frontendMutex.Unlock()

    for client := range frontendClients {
        if err := client.WriteMessage(websocket.TextMessage, data); err != nil {
            log.Printf("[Analyzer] Ошибка отправки данных клиенту: %v", err)
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