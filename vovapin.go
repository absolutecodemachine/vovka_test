package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Configuration constants
const (
	PINNACLE_API_URL  = "https://api.pinnacle.com/"
	PINNACLE_USERNAME = "AG1677099"
	PINNACLE_PASSWORD = "5421123A"
	SPORT_ID          = 29
	LIVE_MODE         = 1
	ODDS_FORMAT       = "Decimal"
	REQUEST_TIMEOUT   = 5 * time.Second
	PROXY             = "http://AllivanService:PinnacleProxy@154.7.188.227:5242"
)

// Global data storage for matches
var (
	matchesData      = make(map[int]MatchData)
	matchesDataMutex sync.RWMutex
)

// MatchData stores match information
type MatchData struct {
	Home      string
	Away      string
	League    string
	StartTime string
}

// PinnacleAPI for API interaction
type PinnacleAPI struct {
	Username string
	Password string
	Client   *http.Client
}

// NewPinnacleAPI initializes a new PinnacleAPI instance
func NewPinnacleAPI(username, password, proxy string) *PinnacleAPI {
	transport := &http.Transport{}

	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			fmt.Printf("[ERROR] Invalid proxy URL: %v\n", err)
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

// buildURL constructs the API endpoint URL with parameters
func (api *PinnacleAPI) buildURL(endpoint string, params map[string]string) string {
	u, err := url.Parse(PINNACLE_API_URL + endpoint)
	if err != nil {
		fmt.Printf("[ERROR] Error parsing URL: %v\n", err)
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

// query makes an HTTP GET request to the specified URL
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

	fmt.Printf("[LOG] Request to URL: %s\n", urlStr)
	fmt.Printf("[LOG] Response status: %d\n", resp.StatusCode)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// fetchMatches updates the global matchesData with live match information
func fetchMatches(api *PinnacleAPI) error {
	params := map[string]string{
		"sportId": fmt.Sprintf("%d", SPORT_ID),
		"isLive":  fmt.Sprintf("%d", LIVE_MODE),
	}

	urlStr := api.buildURL("v1/fixtures", params)
	data, err := api.query(urlStr)
	if err != nil {
		fmt.Printf("[ERROR] Error fetching matches: %v\n", err)
		return err
	}

	leagues, ok := data["league"].([]interface{})
	if !ok {
		fmt.Println("[ERROR] Failed to load match data.")
		return fmt.Errorf("missing 'league' key in response")
	}

	matchesDataMutex.Lock()
	defer matchesDataMutex.Unlock()
	matchesData = make(map[int]MatchData)

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
				eventID := int(eventIDFloat)
				homeTeam, _ := eventMap["home"].(string)
				if homeTeam == "" {
					homeTeam = "Неизвестная команда"
				}
				awayTeam, _ := eventMap["away"].(string)
				if awayTeam == "" {
					awayTeam = "Неизвестная команда"
				}
				startTime, _ := eventMap["starts"].(string)

				matchesData[eventID] = MatchData{
					Home:      homeTeam,
					Away:      awayTeam,
					League:    leagueName,
					StartTime: startTime,
				}
			}
		}
	}

	return nil
}

// Outcome represents a betting outcome
type Outcome interface {
	String() string
}

// MoneylineOutcome represents a moneyline outcome
type MoneylineOutcome struct {
	Market string
	Team   string
	Odds   float64
}

func (o MoneylineOutcome) String() string {
	return fmt.Sprintf("%s - %s: %.2f", o.Market, o.Team, o.Odds)
}

// TotalOutcome represents a total points outcome
type TotalOutcome struct {
	Market    string
	Line      float64
	OverOdds  float64
	UnderOdds float64
}

func (o TotalOutcome) String() string {
	return fmt.Sprintf("%s - Over %.2f: %.2f, Under %.2f: %.2f", o.Market, o.Line, o.OverOdds, o.Line, o.UnderOdds)
}

// SpreadOutcome represents a spread outcome
type SpreadOutcome struct {
	Market    string
	Line      float64
	HomeOdds  float64
	AwayOdds  float64
}

func (o SpreadOutcome) String() string {
	return fmt.Sprintf("%s - Home %.2f: %.2f, Away %.2f: %.2f", o.Market, o.Line, o.HomeOdds, -o.Line, o.AwayOdds)
}

// TeamTotalOutcome represents a team total outcome
type TeamTotalOutcome struct {
	Market    string
	Team      string
	Line      float64
	OverOdds  float64
	UnderOdds float64
}

func (o TeamTotalOutcome) String() string {
	return fmt.Sprintf("%s (%s) - Over %.2f: %.2f, Under %.2f: %.2f", o.Market, o.Team, o.Line, o.OverOdds, o.Line, o.UnderOdds)
}

// ProcessedEvent stores processed event data
type ProcessedEvent struct {
	EventID    int
	MatchName  string
	LeagueName string
	StartTime  string
	HomeTeam   string
	AwayTeam   string
	Outcomes   []Outcome
}

// processMatchData processes event data and extracts outcomes
func processMatchData(eventData map[string]interface{}) (*ProcessedEvent, error) {
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
	eventID := int(eventIDFloat)

	matchesDataMutex.RLock()
	matchData, exists := matchesData[eventID]
	matchesDataMutex.RUnlock()

	var homeTeam, awayTeam, leagueName, startTime string

	if exists {
		homeTeam = matchData.Home
		awayTeam = matchData.Away
		leagueName = matchData.League
		startTime = matchData.StartTime
	} else {
		fmt.Printf("[WARNING] Event %d not found in matchesData.\n", eventID)
		homeTeam = "Неизвестная команда"
		awayTeam = "Неизвестная команда"
		leagueName = "Неизвестная лига"
		startTime = ""
	}

	outcomes := []Outcome{}

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

		periodNumberFloat, _ := periodMap["number"].(float64)
		periodNumber := int(periodNumberFloat)

		periodName := "Матч"
		if periodNumber == 1 {
			periodName = "Первый тайм"
		} else if periodNumber == 2 {
			periodName = "Второй тайм"
		}

		// Moneyline
		if moneyline, ok := periodMap["moneyline"].(map[string]interface{}); ok {
			if odds, exists := moneyline["home"]; exists {
				homeOdds, _ := odds.(float64)
				outcomes = append(outcomes, MoneylineOutcome{
					Market: fmt.Sprintf("Победа (%s)", periodName),
					Team:   "Home",
					Odds:   homeOdds,
				})
			}
			if odds, exists := moneyline["draw"]; exists {
				drawOdds, _ := odds.(float64)
				outcomes = append(outcomes, MoneylineOutcome{
					Market: fmt.Sprintf("Победа (%s)", periodName),
					Team:   "Draw",
					Odds:   drawOdds,
				})
			}
			if odds, exists := moneyline["away"]; exists {
				awayOdds, _ := odds.(float64)
				outcomes = append(outcomes, MoneylineOutcome{
					Market: fmt.Sprintf("Победа (%s)", periodName),
					Team:   "Away",
					Odds:   awayOdds,
				})
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

				outcomes = append(outcomes, TotalOutcome{
					Market:    fmt.Sprintf("Общий тотал (%s)", periodName),
					Line:      points,
					OverOdds:  overOdds,
					UnderOdds: underOdds,
				})
			}
		}

		// Spreads
		if spreads, ok := periodMap["spreads"].([]interface{}); ok {
			for _, spreadInterface := range spreads {
				spreadMap, ok := spreadInterface.(map[string]interface{})
				if !ok {
					continue
				}

				hdp, _ := spreadMap["hdp"].(float64)
				homeOdds, _ := spreadMap["home"].(float64)
				awayOdds, _ := spreadMap["away"].(float64)

				outcomes = append(outcomes, SpreadOutcome{
					Market:   fmt.Sprintf("Гандикап (%s)", periodName),
					Line:     hdp,
					HomeOdds: homeOdds,
					AwayOdds: awayOdds,
				})
			}
		}

		// Team totals
		if teamTotal, ok := periodMap["teamTotal"].(map[string]interface{}); ok {
			for teamKey, totalInterface := range teamTotal {
				totalMap, ok := totalInterface.(map[string]interface{})
				if !ok {
					continue
				}

				points, _ := totalMap["points"].(float64)
				overOdds, _ := totalMap["over"].(float64)
				underOdds, _ := totalMap["under"].(float64)

				teamName := "Home"
				if teamKey == "away" {
					teamName = "Away"
				}

				outcomes = append(outcomes, TeamTotalOutcome{
					Market:    fmt.Sprintf("Индивидуальный тотал (%s)", periodName),
					Team:      teamName,
					Line:      points,
					OverOdds:  overOdds,
					UnderOdds: underOdds,
				})
			}
		}
	}

	processedEvent := &ProcessedEvent{
		EventID:    eventID,
		MatchName:  fmt.Sprintf("%s vs %s", homeTeam, awayTeam),
		LeagueName: leagueName,
		StartTime:  startTime,
		HomeTeam:   homeTeam,
		AwayTeam:   awayTeam,
		Outcomes:   outcomes,
	}

	return processedEvent, nil
}

// getLiveOdds fetches live odds and processes each event
func getLiveOdds(api *PinnacleAPI) error {
	params := map[string]string{
		"sportId":    fmt.Sprintf("%d", SPORT_ID),
		"isLive":     fmt.Sprintf("%d", LIVE_MODE),
		"oddsFormat": ODDS_FORMAT,
	}

	urlStr := api.buildURL("v1/odds", params)
	data, err := api.query(urlStr)
	if err != nil {
		fmt.Printf("[ERROR] API response is empty or contains an error: %v\n", err)
		return err
	}

	fmt.Println("[LOG] API response successfully received.")

	leagues, ok := data["leagues"].([]interface{})
	if !ok {
		fmt.Println("[ERROR] Key 'leagues' is missing in response.")
		fmt.Printf("Response: %v\n", data)
		return fmt.Errorf("missing 'leagues' key in response")
	}

	for _, leagueInterface := range leagues {
		leagueMap, ok := leagueInterface.(map[string]interface{})
		if !ok {
			continue
		}

		leagueName, _ := leagueMap["name"].(string)
		if leagueName == "" {
			leagueName = "Неизвестная лига"
		}
		fmt.Printf("Лига: %s\n", leagueName)

		events, ok := leagueMap["events"].([]interface{})
		if !ok {
			fmt.Printf("[WARNING] В лиге '%s' отсутствуют события.\n", leagueName)
			continue
		}

		for _, eventInterface := range events {
			eventMap, ok := eventInterface.(map[string]interface{})
			if !ok {
				continue
			}

			processedEvent, err := processMatchData(eventMap)
			if err != nil {
				fmt.Printf("[WARNING] Не удалось обработать событие: %v\n", err)
				continue
			}

			fmt.Printf("Матч: %s\n", processedEvent.MatchName)
			fmt.Printf("Время начала: %s\n", processedEvent.StartTime)
			for _, outcome := range processedEvent.Outcomes {
				fmt.Println(outcome.String())
			}
			fmt.Println(strings.Repeat("-", 50))
		}
	}

	return nil
}

// main function runs the program loop
func main() {
	api := NewPinnacleAPI(PINNACLE_USERNAME, PINNACLE_PASSWORD, PROXY)

	for {
		if err := fetchMatches(api); err != nil {
			fmt.Printf("[ERROR] Error in main loop (fetchMatches): %v\n", err)
		}

		if err := getLiveOdds(api); err != nil {
			fmt.Printf("[ERROR] Error in main loop (getLiveOdds): %v\n", err)
		}

		time.Sleep(5 * time.Second)
	}
}
