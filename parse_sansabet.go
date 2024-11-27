package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Константы для URL запросов
const (
	allEventURL   = "https://apilive.sansabet.com/api/LiveOdds/GetAll?SLID=%d"
	matchEventURL = "https://apilive.sansabet.com/api/LiveOdds/GetByParIDs?SLID=%d&ParIDs=%d"
)

// Глобальные переменные
var (
	analyzerConnection *websocket.Conn
	analyzerConnMutex  sync.Mutex

	SlidAll      int64 = 0
	ListGames          = make(map[int64]OneGame)
	ListGamesMux sync.RWMutex
)

// Структуры данных
type OneGame struct {
	Source     string `json:"Source"`
	Name       string `json:"Name"`
	Pid        int64  `json:"Pid"`
	Slid       int64  `json:"Slid"`
	LeagueName string `json:"LeagueName"`
	MatchName  string `json:"MatchName"`
	MatchId    string `json:"MatchId"`
	LeagueId   string `json:"LeagueId"`

	Win1x2           Win1x2Struct           `json:"Win1x2"`
	Totals           map[string]WinLessMore `json:"Totals"`
	Handicap         map[string]WinHandicap `json:"Handicap"`
	FirstTeamTotals  map[string]WinLessMore `json:"FirstTeamTotals"`
	SecondTeamTotals map[string]WinLessMore `json:"SecondTeamTotals"`

	Time1Win1x2           Win1x2Struct           `json:"Time1Win1x2"`
	Time1Totals           map[string]WinLessMore `json:"Time1Totals"`
	Time1Handicap         map[string]WinHandicap `json:"Time1Handicap"`
	Time1FirstTeamTotals  map[string]WinLessMore `json:"Time1FirstTeam"`
	Time1SecondTeamTotals map[string]WinLessMore `json:"Time1SecondTeam"`

	Time2Win1x2           Win1x2Struct           `json:"Time2Win1x2"`
	Time2Totals           map[string]WinLessMore `json:"Time2Totals"`
	Time2Handicap         map[string]WinHandicap `json:"Time2Handicap"`
	Time2FirstTeamTotals  map[string]WinLessMore `json:"Time2FirstTeam"`
	Time2SecondTeamTotals map[string]WinLessMore `json:"Time2SecondTeam"`
}

type WinLessMore struct {
	WinMore float64 `json:"WinMore"`
	WinLess float64 `json:"WinLess"`
}

type WinHandicap struct {
	Win1 float64 `json:"Win1"`
	Win2 float64 `json:"Win2"`
}

type Win1x2Struct struct {
	Win1    float64 `json:"Win1"`
	WinNone float64 `json:"WinNone"`
	Win2    float64 `json:"Win2"`
}

// Вспомогательные функции
func InterfaceToInt64(inVal interface{}) int64 {
	if resInt, ok := inVal.(float64); ok {
		return int64(resInt)
	}
	return -1
}

func StringToInt64(inVal string) int64 {
	if i, err := strconv.ParseInt(inVal, 10, 64); err == nil {
		return i
	}
	return -1
}

func DebugLog(str string) {
	// fmt.Println(str)
}

// Основная функция парсинга одного матча
func ParseOneGame(nGameKey int64, nGame OneGame) {
	// DebugLog(fmt.Sprintf("Начало анализа Slid: %d Pid: %d", nGame.Slid, nGame.Pid))
	start := time.Now() // Начало замера времени

	client := &http.Client{}
	// мы заменили слид на ноль, чтобы получать данные даже если цены не изменились
	req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventURL, 0, nGame.Pid), nil)
	//req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventURL, nGame.Slid, nGame.Pid), nil)

	// Установка заголовков запроса
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://sansabet.com")
	req.Header.Set("Referer", "https://sansabet.com/")
	req.Header.Set("Sec-Ch-Ua", "\"Google Chrome\";v=\"123\", \"Not:A-Brand\";v=\"8\", \"Chromium\";v=\"123\"")
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", "\"Linux\"")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")

	res, err := client.Do(req)
	elapsed := time.Since(start)
	log.Printf("[INFO] Время получения данных для матча (Slid: %d, Pid: %d): %s", nGame.Slid, nGame.Pid, elapsed)

	if err != nil {
		DebugLog(fmt.Sprintf("Ошибка запроса: %v", err))
		return
	}
	defer res.Body.Close()

	if res.Body == nil {
		DebugLog(fmt.Sprintf("Нет данных по запросу о матче. Slid %d Pid %d", nGame.Slid, nGame.Pid))
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		DebugLog(fmt.Sprintf("Ошибка чтения тела ответа: %v", err))
		return
	}

	b := bytes.NewBuffer(body)
	r, err := gzip.NewReader(b)
	if err != nil {
		DebugLog(fmt.Sprintf("Ошибка создания gzip reader: %v", err))
		return
	}
	defer r.Close()

	var resB bytes.Buffer
	_, err = resB.ReadFrom(r)
	if err != nil {
		DebugLog(fmt.Sprintf("Ошибка чтения из gzip reader: %v", err))
		return
	}

	resData := resB.Bytes()
	log.Printf("[DEBUG] Полученные данные для матча (Slid: %d, Pid: %d): %s", nGame.Slid, nGame.Pid, string(resData))
	if len(resData) == 0 {
		log.Printf("[WARNING] Пустой ответ для матча (Slid: %d, Pid: %d)", nGame.Slid, nGame.Pid)
		return
	}

	// Обработка данных матча
	var mapAllInterface []interface{}
	jsonErr := json.Unmarshal(resData, &mapAllInterface)
	if jsonErr != nil {
		// DebugLog(fmt.Sprintf("Ошибка разбора JSON: %v", jsonErr))
		return
	}

	// Проверка на пустой ответ
	if len(mapAllInterface) == 0 {
		// DebugLog(fmt.Sprintf("Пустой ответ от сервера для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
		return
	}

	nOneGame := nGame

	mapCoreItemInterface, _ := mapAllInterface[0].(map[string]interface{})
	mapHInterface, _ := mapCoreItemInterface["H"].(map[string]interface{})

	// Заполнение полей матча
	nOneGame.LeagueName, _ = mapHInterface["LigaNaziv"].(string)
	nOneGame.Slid = InterfaceToInt64(mapHInterface["SLID"])
	nOneGame.MatchName, _ = mapHInterface["ParNaziv"].(string)
	nOneGame.Source = "Sansabet"
	nOneGame.MatchId = strconv.FormatInt(nOneGame.Pid, 10)
	nOneGame.LeagueId = "0"

	ListGamesMux.Lock()
	ListGames[nGameKey] = nOneGame
	ListGamesMux.Unlock()

	// Проверка наличия коэффициентов
	if mapCoreItemInterface["M"] == nil {
		// DebugLog(fmt.Sprintf("Нет коэффициентов для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
		return
	}

	// Инициализация структур для хранения коэффициентов
	mapKoeffInterface := mapCoreItemInterface["M"].([]interface{})
	Win1x2 := Win1x2Struct{Win1: 0, Win2: 0, WinNone: 0}
	Totals := make(map[string]WinLessMore)
	Handicap := make(map[string]WinHandicap)
	FirstTeamTotals := make(map[string]WinLessMore)
	SecondTeamTotals := make(map[string]WinLessMore)

	Time1Win1x2 := Win1x2Struct{Win1: 0, Win2: 0, WinNone: 0}
	Time1Totals := make(map[string]WinLessMore)
	Time1Handicap := make(map[string]WinHandicap)
	Time1FirstTeamTotals := make(map[string]WinLessMore)
	Time1SecondTeamTotals := make(map[string]WinLessMore)

	Time2Win1x2 := Win1x2Struct{Win1: 0, Win2: 0, WinNone: 0}
	Time2Totals := make(map[string]WinLessMore)
	Time2Handicap := make(map[string]WinHandicap)
	Time2FirstTeamTotals := make(map[string]WinLessMore)
	Time2SecondTeamTotals := make(map[string]WinLessMore)

	// Парсинг коэффициентов
	for _, nKoef := range mapKoeffInterface {
		nKoefMap := nKoef.(map[string]interface{})
		mapValBet, ok := nKoefMap["S"].([]interface{})
		if !ok {
			continue
		}

		for _, nValBet := range mapValBet {
			nValBetMap := nValBet.(map[string]interface{})
			typeBet := int64(nValBetMap["N"].(float64))
			detailBet := ""
			if nKoefMap["B"] != nil {
				detailBet = nKoefMap["B"].(string)
			}

			switch typeBet {
			case 1:
				Win1x2.Win1 = nValBetMap["O"].(float64)
			case 2:
				Win1x2.WinNone = nValBetMap["O"].(float64)
			case 10:
				Win1x2.Win2 = nValBetMap["O"].(float64)
			case 103:
				if _, ok := Totals[detailBet]; !ok {
					Totals[detailBet] = WinLessMore{}
				}
				nK := Totals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				Totals[detailBet] = nK
			case 105:
				if _, ok := Totals[detailBet]; !ok {
					Totals[detailBet] = WinLessMore{}
				}
				nK := Totals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				Totals[detailBet] = nK
			case 169:
				if _, ok := FirstTeamTotals[detailBet]; !ok {
					FirstTeamTotals[detailBet] = WinLessMore{}
				}
				nK := FirstTeamTotals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				FirstTeamTotals[detailBet] = nK
			case 168:
				if _, ok := FirstTeamTotals[detailBet]; !ok {
					FirstTeamTotals[detailBet] = WinLessMore{}
				}
				nK := FirstTeamTotals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				FirstTeamTotals[detailBet] = nK
			case 171:
				if _, ok := SecondTeamTotals[detailBet]; !ok {
					SecondTeamTotals[detailBet] = WinLessMore{}
				}
				nK := SecondTeamTotals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				SecondTeamTotals[detailBet] = nK
			case 170:
				if _, ok := SecondTeamTotals[detailBet]; !ok {
					SecondTeamTotals[detailBet] = WinLessMore{}
				}
				nK := SecondTeamTotals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				SecondTeamTotals[detailBet] = nK
			case 121:
				conv, _ := strconv.ParseFloat(detailBet, 64)
				detailBet = strconv.FormatFloat(conv-0.5, 'f', 1, 64)
				if _, ok := Handicap[detailBet]; !ok {
					Handicap[detailBet] = WinHandicap{}
				}
				nK := Handicap[detailBet]
				nK.Win1 = nValBetMap["O"].(float64)
				Handicap[detailBet] = nK
			case 123:
				conv, _ := strconv.ParseFloat(detailBet, 64)
				detailBet = strconv.FormatFloat((-1*conv)-0.5, 'f', 1, 64)
				if _, ok := Handicap[detailBet]; !ok {
					Handicap[detailBet] = WinHandicap{}
				}
				nK := Handicap[detailBet]
				nK.Win2 = nValBetMap["O"].(float64)
				Handicap[detailBet] = nK

			// double chance
			case 83:
				if _, ok := Handicap["0.5"]; !ok {
					Handicap["0.5"] = WinHandicap{}
				}
				nK := Handicap["0.5"]
				nK.Win1 = nValBetMap["O"].(float64)
				Handicap["0.5"] = nK
			case 85:
				if _, ok := Handicap["0.5"]; !ok {
					Handicap["0.5"] = WinHandicap{}
				}
				nK := Handicap[detailBet]
				nK.Win2 = nValBetMap["O"].(float64)
				Handicap["0.5"] = nK

			// rest of the match
			case 734:
				scores := strings.Split(nKoefMap["R"].(string), "-")
				score1, _ := strconv.ParseFloat(scores[0], 64)
				score2, _ := strconv.ParseFloat(scores[1], 64)
				hcpLine := strconv.FormatFloat((score2-score1)-0.5, 'f', 1, 64)
				if _, ok := Handicap[hcpLine]; !ok {
					Handicap[hcpLine] = WinHandicap{}
				}
				nK := Handicap[hcpLine]
				nK.Win1 = nValBetMap["O"].(float64)
				Handicap[hcpLine] = nK
			case 736:
				scores := strings.Split(nKoefMap["R"].(string), "-")
				score1, _ := strconv.ParseFloat(scores[0], 64)
				score2, _ := strconv.ParseFloat(scores[1], 64)
				hcpLine := strconv.FormatFloat((score1-score2)-0.5, 'f', 1, 64)
				if _, ok := Handicap[hcpLine]; !ok {
					Handicap[hcpLine] = WinHandicap{}
				}
				nK := Handicap[hcpLine]
				nK.Win2 = nValBetMap["O"].(float64)
				Handicap[hcpLine] = nK

			// 1 time
			case 93:
				Time1Win1x2.Win1 = nValBetMap["O"].(float64)
			case 94:
				Time1Win1x2.WinNone = nValBetMap["O"].(float64)
			case 95:
				Time1Win1x2.Win2 = nValBetMap["O"].(float64)
			case 165:
				if _, ok := Time1Totals[detailBet]; !ok {
					Time1Totals[detailBet] = WinLessMore{}
				}
				nK := Time1Totals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				Time1Totals[detailBet] = nK
			case 167:
				if _, ok := Time1Totals[detailBet]; !ok {
					Time1Totals[detailBet] = WinLessMore{}
				}
				nK := Time1Totals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				Time1Totals[detailBet] = nK
			case 746:
				if _, ok := Time1FirstTeamTotals[detailBet]; !ok {
					Time1FirstTeamTotals[detailBet] = WinLessMore{}
				}
				nK := Time1FirstTeamTotals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				Time1FirstTeamTotals[detailBet] = nK
			case 747:
				if _, ok := Time1FirstTeamTotals[detailBet]; !ok {
					Time1FirstTeamTotals[detailBet] = WinLessMore{}
				}
				nK := Time1FirstTeamTotals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				Time1FirstTeamTotals[detailBet] = nK
			case 748:
				if _, ok := Time1SecondTeamTotals[detailBet]; !ok {
					Time1SecondTeamTotals[detailBet] = WinLessMore{}
				}
				nK := Time1SecondTeamTotals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				Time1SecondTeamTotals[detailBet] = nK
			case 749:
				if _, ok := Time1SecondTeamTotals[detailBet]; !ok {
					Time1SecondTeamTotals[detailBet] = WinLessMore{}
				}
				nK := Time1SecondTeamTotals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				Time1SecondTeamTotals[detailBet] = nK

			// rest of the match time 1
			case 737:
				scores := strings.Split(nKoefMap["R"].(string), "-")
				score1, _ := strconv.ParseFloat(scores[0], 64)
				score2, _ := strconv.ParseFloat(scores[1], 64)
				hcpLine := strconv.FormatFloat((score2-score1)-0.5, 'f', 1, 64)
				if _, ok := Time1Handicap[hcpLine]; !ok {
					Time1Handicap[hcpLine] = WinHandicap{}
				}
				nK := Time1Handicap[hcpLine]
				nK.Win1 = nValBetMap["O"].(float64)
				Time1Handicap[hcpLine] = nK
			case 739:
				scores := strings.Split(nKoefMap["R"].(string), "-")
				score1, _ := strconv.ParseFloat(scores[0], 64)
				score2, _ := strconv.ParseFloat(scores[1], 64)
				hcpLine := strconv.FormatFloat((score1-score2)-0.5, 'f', 1, 64)
				if _, ok := Time1Handicap[hcpLine]; !ok {
					Time1Handicap[hcpLine] = WinHandicap{}
				}
				nK := Time1Handicap[hcpLine]
				nK.Win2 = nValBetMap["O"].(float64)
				Time1Handicap[hcpLine] = nK

			// 2 time
			case 96:
				Time2Win1x2.Win1 = nValBetMap["O"].(float64)
			case 97:
				Time2Win1x2.WinNone = nValBetMap["O"].(float64)
			case 98:
				Time2Win1x2.Win2 = nValBetMap["O"].(float64)
			case 754:
				if _, ok := Time2Totals[detailBet]; !ok {
					Time2Totals[detailBet] = WinLessMore{}
				}
				nK := Time2Totals[detailBet]
				nK.WinLess = nValBetMap["O"].(float64)
				Time2Totals[detailBet] = nK
			case 755:
				if _, ok := Time2Totals[detailBet]; !ok {
					Time2Totals[detailBet] = WinLessMore{}
				}
				nK := Time2Totals[detailBet]
				nK.WinMore = nValBetMap["O"].(float64)
				Time2Totals[detailBet] = nK

			default:
				// Другие типы ставок не обрабатываем
			}
		}
	}

	// Присвоение коэффициентов матчу
	nOneGame.Win1x2 = Win1x2
	nOneGame.Totals = Totals
	nOneGame.Handicap = Handicap
	nOneGame.FirstTeamTotals = FirstTeamTotals
	nOneGame.SecondTeamTotals = SecondTeamTotals

	nOneGame.Time1Win1x2 = Time1Win1x2
	nOneGame.Time1Totals = Time1Totals
	nOneGame.Time1Handicap = Time1Handicap
	nOneGame.Time1FirstTeamTotals = Time1FirstTeamTotals
	nOneGame.Time1SecondTeamTotals = Time1SecondTeamTotals

	nOneGame.Time2Win1x2 = Time2Win1x2
	nOneGame.Time2Totals = Time2Totals
	nOneGame.Time2Handicap = Time2Handicap
	nOneGame.Time2FirstTeamTotals = Time2FirstTeamTotals
	nOneGame.Time2SecondTeamTotals = Time2SecondTeamTotals

	// Преобразование в JSON
	jsonResult, err := json.MarshalIndent(nOneGame, "", "    ")
	if err != nil {
		// DebugLog(fmt.Sprintf("Не удалось сформировать JSON для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
		return
	}

	fmt.Println(string(jsonResult))

	// Отправка данных в анализатор
	fmt.Printf("[LOG] Отправляем сообщение в анализатор: %s\n", string(jsonResult))
	analyzerConnMutex.Lock()
	err = analyzerConnection.WriteMessage(websocket.TextMessage, jsonResult)
	analyzerConnMutex.Unlock()
	if err != nil {
		log.Printf("Ошибка отправки сообщения: %v", err)
	}

	// Обновление списка игр
	ListGamesMux.Lock()
	ListGames[nGameKey] = nOneGame
	ListGamesMux.Unlock()
}

// Функция парсинга всех игр
func ParseGames() {
	// DebugLog("Запуск парсинга всех матчей")
	ListGamesMux.RLock()
	defer ListGamesMux.RUnlock()
	for nGameKey, nGame := range ListGames {
		go ParseOneGame(nGameKey, nGame)
	}
}

// Функция получения всех событий
func ParseEvents() {
	// DebugLog("Получение данных о всех матчах")
	client := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf(allEventURL, SlidAll), nil)

	// Установка заголовков запроса
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://sansabet.com")
	req.Header.Set("Referer", "https://sansabet.com/")
	req.Header.Set("Sec-Ch-Ua", "\"Google Chrome\";v=\"123\", \"Not:A-Brand\";v=\"8\", \"Chromium\";v=\"123\"")
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", "\"Linux\"")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64)")

	res, err := client.Do(req)
	if err != nil {
		// DebugLog(fmt.Sprintf("Ошибка запроса: %v", err))
		return
	}
	defer res.Body.Close()

	if res.Body == nil {
		// DebugLog("Нет ответа от сервера для всех матчей")
		return
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		// DebugLog(fmt.Sprintf("Ошибка чтения тела ответа: %v", err))
		return
	}

	b := bytes.NewBuffer(body)
	r, err := gzip.NewReader(b)
	if err != nil {
		// DebugLog(fmt.Sprintf("Ошибка создания gzip reader: %v", err))
		return
	}
	defer r.Close()

	var resB bytes.Buffer
	_, err = resB.ReadFrom(r)
	if err != nil {
		// DebugLog(fmt.Sprintf("Ошибка чтения из gzip reader: %v", err))
		return
	}
	resData := resB.Bytes()

	var mapAllInterface []interface{}
	jsonErr := json.Unmarshal(resData, &mapAllInterface)
	if jsonErr != nil {
		// DebugLog("Не удалось разобрать JSON с данными всех матчей")
		return
	}

	for _, nAllInterface := range mapAllInterface {
		allInterfaceMap := nAllInterface.(map[string]interface{})
		hMap := allInterfaceMap["H"].(map[string]interface{})

		nSlid := InterfaceToInt64(hMap["SLID"])
		nPid := InterfaceToInt64(hMap["PID"])

		if nSlid > SlidAll {
			SlidAll = nSlid
		}

		// Фильтруем только футбол
		if hMap["S"].(string) != "F" {
			continue
		}

		// Получение времени матча
		pMap := allInterfaceMap["P"].(map[string]interface{})
		tMap := pMap["T"].(map[string]interface{})
		nTime := int64(-2)
		if tMap["M"] != nil {
			nTime = StringToInt64(tMap["M"].(string))
		}

		// Фильтрация по времени матча (лайв)
		if nTime <= 0 || nTime >= 90 {
			continue
		}

		ListGamesMux.Lock()
		if _, ok := ListGames[nPid]; !ok {
			ListGames[nPid] = OneGame{
				Pid:     nPid,
				Slid:    0,
				MatchId: strconv.FormatInt(nPid, 10),
			}
		}
		ListGamesMux.Unlock()
	}
	// DebugLog(fmt.Sprintf("Обновление SLID: %d, количество матчей: %d", SlidAll, len(ListGames)))
}

// Запуск парсинга событий
func ParseEventsStart() {
	for {
		ParseEvents()
		time.Sleep(2 * time.Second)
	}
}

// Запуск парсинга игр
func ParseGamesStart() {
	for {
		ParseGames()
		time.Sleep(2 * time.Second)
	}
}

// Функция подключения к анализатору
func connectToAnalyzer() {
	for {
		var err error
		analyzerConnection, _, err = websocket.DefaultDialer.Dial("ws://localhost:7100", nil)
		if err != nil {
			log.Printf("[ERROR] Ошибка подключения к анализатору: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		// log.Printf("[DEBUG] Подключение к анализатору установлено")
		break
	}
}

// Основная функция
func main() {
	// DebugLog("Старт скрипта")
	ListGames = make(map[int64]OneGame)

	// Подключение к анализатору
	connectToAnalyzer()
	defer analyzerConnection.Close()

	// Запуск парсинга в горутинах
	go ParseEventsStart()
	go ParseGamesStart()

	// Основной цикл
	select {}
}
