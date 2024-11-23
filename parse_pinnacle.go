package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    "log"
    "net/http"
    "net/url"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

// Константы конфигурации
const (
    PINNACLE_API_URL = "https://api.pinnacle.com/"
    SPORT_ID         = 29
    LIVE_MODE        = 1
    ODDS_FORMAT      = "Decimal"
    REQUEST_TIMEOUT  = 1 * time.Second
    SINCE = 0 // важный параметр, чтобы цены передавались актуальные
)

// Замените на ваши данные
const (
    PINNACLE_USERNAME = "AG1677099"
    PINNACLE_PASSWORD = "5421123A"
    PROXY             = "http://AllivanService:PinnacleProxy@154.7.188.227:5242"
)

// Глобальные переменные
var (
    analyzerConnection *websocket.Conn
    matchesData        = make(map[int64]MatchData)
    matchesDataMutex   sync.RWMutex
)

// Структуры данных
type OneGame struct {
    Source           string                 `json:"Source"`
    Name             string                 `json:"Name"`
    Pid              int64                  `json:"Pid"`
    Slid             int64                  `json:"Slid"`
    LeagueName       string                 `json:"LeagueName"`
    MatchName        string                 `json:"MatchName"`
    MatchId          string                 `json:"MatchId"`
    LeagueId         string                 `json:"LeagueId"`
    Win1x2           Win1x2Struct           `json:"Win1x2"`
    Totals           map[string]WinLessMore `json:"Totals"`
    Handicap         map[string]WinHandicap `json:"Handicap"`
    FirstTeamTotals  map[string]WinLessMore `json:"FirstTeamTotals"`
    SecondTeamTotals map[string]WinLessMore `json:"SecondTeamTotals"`
}

type WinLessMore struct {
    WinMore float64 `json:"WinMore"`
    WinLess float64 `json:"WinLess"`
}

type WinHandicap struct {
    Win1O    float64 `json:"Win1O"`
    WinNoneO float64 `json:"WinNoneO"`
    Win2O    float64 `json:"Win2O"`
    Win1     float64 `json:"Win1"`
    WinNone  float64 `json:"WinNone"`
    Win2     float64 `json:"Win2"`
}

type Win1x2Struct struct {
    Win1    float64 `json:"Win1"`
    WinNone float64 `json:"WinNone"`
    Win2    float64 `json:"Win2"`
}

type MatchData struct {
    Home      string
    Away      string
    League    string
    StartTime string
}

// PinnacleAPI для взаимодействия с API
type PinnacleAPI struct {
    Username string
    Password string
    Client   *http.Client
}

// Вспомогательные функции
func NewPinnacleAPI(username, password, proxy string) *PinnacleAPI {
    transport := &http.Transport{}

    if proxy != "" {
        proxyURL, err := url.Parse(proxy)
        if err != nil {
            fmt.Printf("[ERROR] Неверный URL прокси: %v\n", err)
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

func (api *PinnacleAPI) buildURL(endpoint string, params map[string]string) string {
    u, err := url.Parse(PINNACLE_API_URL + endpoint)
    if err != nil {
        fmt.Printf("[ERROR] Ошибка парсинга URL: %v\n", err)
        return ""
    }

    query := u.Query()
    for k, v := range params {
        if v != "" {
            query.Set(k, v)
        }
    }
    u.RawQuery = query.Encode()
    return u.String()
}

func (api *PinnacleAPI) query(urlStr string) (map[string]interface{}, error) {
    req, err := http.NewRequest("GET", urlStr, nil)
    if err != nil {
        return nil, err
    }

    req.SetBasicAuth(api.Username, api.Password)
    req.Header.Set("Accept", "application/json")
    req.Header.Set("Content-Type", "application/json")

    resp, err := api.Client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    // fmt.Printf("[LOG] Запрос к URL: %s\n", urlStr)
    // fmt.Printf("[LOG] Статус ответа: %d\n", resp.StatusCode)

    body, err := ioutil.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }

    fmt.Printf("[DEBUG] Raw API response: %s\n", string(body))

    var result map[string]interface{}
    if err := json.Unmarshal(body, &result); err != nil {
        return nil, err
    }

    return result, nil
}

// Функция для обновления данных матчей
func fetchMatches(api *PinnacleAPI) error {
    params := map[string]string{
        "sportId": fmt.Sprintf("%d", SPORT_ID),
        "isLive":  fmt.Sprintf("%d", LIVE_MODE),
        "since":  fmt.Sprintf("%d", SINCE),
    }

    urlStr := api.buildURL("v1/fixtures", params)
    data, err := api.query(urlStr)
    if err != nil {
        fmt.Printf("[ERROR] Ошибка при получении матчей: %v\n", err)
        return err
    }

    leagues, ok := data["league"].([]interface{})
    if !ok {
        fmt.Println("[ERROR] Не удалось загрузить данные матчей.")
        return fmt.Errorf("отсутствует ключ 'league' в ответе")
    }

    matchesDataMutex.Lock()
    defer matchesDataMutex.Unlock()
    matchesData = make(map[int64]MatchData)

    for _, leagueInterface := range leagues {
        leagueMap, ok := leagueInterface.(map[string]interface{})
        if !ok {
            continue
        }
        leagueName, _ := leagueMap["name"].(string)
        if leagueName == "" {
            leagueName = "Неизвестная лига"
        }

        events, ok := leagueMap["events"].([]interface{})
        if !ok {
            continue
        }

        for _, eventInterface := range events {
            eventMap, ok := eventInterface.(map[string]interface{})
            if !ok {
                continue
            }

            liveStatus, _ := eventMap["liveStatus"].(float64)
            if int(liveStatus) == 1 {
                eventIDFloat, _ := eventMap["id"].(float64)
                eventID := int64(eventIDFloat)
                homeTeam, _ := eventMap["home"].(string)
                if homeTeam == "" {
                    homeTeam = "Неизвестная команда"
                }
                awayTeam, _ := eventMap["away"].(string)
                if awayTeam == "" {
                    awayTeam = "Неизвестная команда"
                }

                matchesData[eventID] = MatchData{
                    Home:      homeTeam,
                    Away:      awayTeam,
                    League:    leagueName,
                    StartTime: "",
                }
            }
        }
    }

    return nil
}

// Функция обработки данных матча
func processMatchData(eventData map[string]interface{}) (*OneGame, error) {
    defer func() {
        if r := recover(); r != nil {
            fmt.Printf("[ERROR] Panic in processMatchData: %v\n", r)
            fmt.Printf("Event data: %v\n", eventData)
        }
    }()

    eventIDFloat, ok := eventData["id"].(float64)
    if !ok {
        return nil, fmt.Errorf("event ID not found")
    }
    eventID := int64(eventIDFloat)

    matchesDataMutex.RLock()
    matchData, exists := matchesData[eventID]
    matchesDataMutex.RUnlock()

    var homeTeam, awayTeam, leagueName string

    if exists {
        homeTeam = matchData.Home
        awayTeam = matchData.Away
        leagueName = matchData.League
    } else {
        // fmt.Printf("[WARNING] Событие %d не найдено в matchesData.\n", eventID)
        homeTeam = "Неизвестная команда"
        awayTeam = "Неизвестная команда"
        leagueName = "Неизвестная лига"
    }

    nOneGame := OneGame{
        Name:       fmt.Sprintf("%s vs %s", homeTeam, awayTeam),
        Pid:        eventID,
        Slid:       0,
        LeagueName: leagueName,
        MatchName:  fmt.Sprintf("%s vs %s", homeTeam, awayTeam),
        MatchId:    fmt.Sprintf("%d", eventID),
        LeagueId:   "0",
    }

    Win1x2 := Win1x2Struct{}
    Totals := make(map[string]WinLessMore)
    Handicap := make(map[string]WinHandicap)
    FirstTeamTotals := make(map[string]WinLessMore)
    SecondTeamTotals := make(map[string]WinLessMore)

    periods, _ := eventData["periods"].([]interface{})
    for _, periodInterface := range periods {
        periodMap, ok := periodInterface.(map[string]interface{})
        if !ok {
            continue
        }

        statusFloat, _ := periodMap["status"].(float64)
        if int(statusFloat) != 1 {
            continue
        }

        // Moneyline (Win1x2)
        if moneyline, ok := periodMap["moneyline"].(map[string]interface{}); ok {
            if odds, exists := moneyline["home"]; exists {
                homeOdds, _ := odds.(float64)
                Win1x2.Win1 = homeOdds
            }
            if odds, exists := moneyline["draw"]; exists {
                drawOdds, _ := odds.(float64)
                Win1x2.WinNone = drawOdds
            }
            if odds, exists := moneyline["away"]; exists {
                awayOdds, _ := odds.(float64)
                Win1x2.Win2 = awayOdds
            }
        }

        // Totals
        if totals, ok := periodMap["totals"].([]interface{}); ok {
            for _, totalInterface := range totals {
                totalMap, ok := totalInterface.(map[string]interface{})
                if !ok {
                    continue
                }

                points, _ := totalMap["points"].(float64)
                overOdds, _ := totalMap["over"].(float64)
                underOdds, _ := totalMap["under"].(float64)
                detailBet := fmt.Sprintf("%.2f", points)
                Totals[detailBet] = WinLessMore{
                    WinMore: overOdds,
                    WinLess: underOdds,
                }
            }
        }

        // Spreads (Handicap)
        if spreads, ok := periodMap["spreads"].([]interface{}); ok {
            for _, spreadInterface := range spreads {
                spreadMap, ok := spreadInterface.(map[string]interface{})
                if !ok {
                    continue
                }

                hdp, _ := spreadMap["hdp"].(float64)
                homeOdds, _ := spreadMap["home"].(float64)
                awayOdds, _ := spreadMap["away"].(float64)
                detailBet := fmt.Sprintf("%.2f", hdp)

                Handicap[detailBet] = WinHandicap{
                    Win1:    homeOdds,
                    Win2:    awayOdds,
                    Win1O:   0,
                    Win2O:   0,
                    WinNone: 0,
                }
            }
        }

        // Team Totals
        if teamTotal, ok := periodMap["teamTotal"].(map[string]interface{}); ok {
            for teamKey, totalInterface := range teamTotal {
                totalMap, ok := totalInterface.(map[string]interface{})
                if !ok {
                    continue
                }

                points, _ := totalMap["points"].(float64)
                overOdds, _ := totalMap["over"].(float64)
                underOdds, _ := totalMap["under"].(float64)
                detailBet := fmt.Sprintf("%.2f", points)

                if teamKey == "home" {
                    FirstTeamTotals[detailBet] = WinLessMore{
                        WinMore: overOdds,
                        WinLess: underOdds,
                    }
                } else if teamKey == "away" {
                    SecondTeamTotals[detailBet] = WinLessMore{
                        WinMore: overOdds,
                        WinLess: underOdds,
                    }
                }
            }
        }
    }

    // Присвоение коэффициентов матчу
    nOneGame.Source = "Pinnacle"
    nOneGame.Win1x2 = Win1x2
    nOneGame.Totals = Totals
    nOneGame.Handicap = Handicap
    nOneGame.FirstTeamTotals = FirstTeamTotals
    nOneGame.SecondTeamTotals = SecondTeamTotals

    return &nOneGame, nil
}

// Функция получения текущих коэффициентов и отправки данных
func getLiveOdds(api *PinnacleAPI) error {
    params := map[string]string{
        "sportId":    fmt.Sprintf("%d", SPORT_ID),
        "isLive":     fmt.Sprintf("%d", LIVE_MODE),
        "oddsFormat": ODDS_FORMAT,
    }

    urlStr := api.buildURL("v1/odds", params)
    data, err := api.query(urlStr)
    if err != nil {
        fmt.Printf("[ERROR] Ошибка получения коэффициентов: %v\n", err)
        return err
    }

    leagues, ok := data["leagues"].([]interface{})
    if !ok {
        fmt.Println("[ERROR] Ключ 'leagues' отсутствует в ответе.")
        fmt.Printf("Ответ: %v\n", data)
        return fmt.Errorf("отсутствует ключ 'leagues' в ответе")
    }

    for _, leagueInterface := range leagues {
        leagueMap, ok := leagueInterface.(map[string]interface{})
        if !ok {
            continue
        }

        events, ok := leagueMap["events"].([]interface{})
        if !ok {
            // fmt.Printf("[WARNING] В лиге отсутствуют события.\n")
            continue
        }

        for _, eventInterface := range events {
            eventMap, ok := eventInterface.(map[string]interface{})
            if !ok {
                continue
            }

            nOneGame, err := processMatchData(eventMap)
            if err != nil {
                // fmt.Printf("[WARNING] Не удалось обработать событие: %v\n", err)
                continue
            }

            // Преобразование в JSON
            jsonResult, err := json.MarshalIndent(nOneGame, "", "    ")
            if err != nil {
                // fmt.Printf("[ERROR] Не удалось преобразовать данные в JSON: %v\n", err)
                continue
            }

            // Отправка данных в анализатор
            fmt.Printf("[LOG] Отправляем сообщение в анализатор: %s\n", string(jsonResult))
            err = analyzerConnection.WriteMessage(websocket.TextMessage, jsonResult)
            if err != nil {
                log.Printf("Ошибка отправки сообщения: %v", err)
            }

            fmt.Println(string(jsonResult))
        }
    }

    return nil
}

// Функция подключения к анализатору
func connectToAnalyzer() {
    for {
        var err error
        analyzerConnection, _, err = websocket.DefaultDialer.Dial("ws://localhost:7200", nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения к анализатору: %v", err)
            time.Sleep(5 * time.Second)
            continue
        }
        // log.Printf("[DEBUG] Подключение к анализатору установлено")
        break
    }
}

// Основная функция
func main() {
    api := NewPinnacleAPI(PINNACLE_USERNAME, PINNACLE_PASSWORD, PROXY)

    // Подключение к анализатору
    connectToAnalyzer()
    defer analyzerConnection.Close()

    for {
        if err := fetchMatches(api); err != nil {
            fmt.Printf("[ERROR] Ошибка в цикле (fetchMatches): %v\n", err)
        }

        if err := getLiveOdds(api); err != nil {
            fmt.Printf("[ERROR] Ошибка в цикле (getLiveOdds): %v\n", err)
        }

        time.Sleep(2 * time.Second)
    }
}
