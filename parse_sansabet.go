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

    client := &http.Client{}
    req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventURL, nGame.Slid, nGame.Pid), nil)

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
        // DebugLog(fmt.Sprintf("Нет данных по запросу о матче. Slid %d Pid %d", nGame.Slid, nGame.Pid))
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
            case 734:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win1O = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
            case 735:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.WinNoneO = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
            case 736:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win2O = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
            case 737:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win1 = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
            case 738:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.WinNone = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
            case 739:
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win2 = nValBetMap["O"].(float64)
                Handicap[detailBet] = nK
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

    // Преобразование в JSON
    jsonResult, err := json.MarshalIndent(nOneGame, "", "    ")
    if err != nil {
        // DebugLog(fmt.Sprintf("Не удалось сформировать JSON для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    fmt.Println(string(jsonResult))

    // Отправка данных в анализатор
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
