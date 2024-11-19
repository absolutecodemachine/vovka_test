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

var analyzerConnection *websocket.Conn
var analyzerConnMutex sync.Mutex


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

type RequestGame struct {
    Slid  string
    Parid string
}

const allEventUrl = "https://apilive.sansabet.com/api/LiveOdds/GetAll?SLID=%d"
const matchEventUrl = "https://apilive.sansabet.com/api/LiveOdds/GetByParIDs?SLID=%d&ParIDs=%d"

var (
    SlidAll      = int64(0)
    ListGames    = make(map[int64]OneGame)
    ListGamesMux sync.RWMutex
)

// Вспомогательные функции
func InterfaceToInt64(inVal interface{}) int64 {
    resInt, ok := inVal.(float64)
    if ok {
        return int64(resInt)
    }
    return -1
}

func StringToInt64(inVal string) int64 {
    i, err := strconv.ParseInt(inVal, 10, 64)
    if err == nil {
        return i
    }
    return -1
}

func DebugLog(str string) {
    fmt.Println(str)
}

// Основная функция парсинга одного матча
func ParseOneGame(nGameKey int64, nGame OneGame) {
    DebugLog(fmt.Sprintf("Начало анализа Slid: %d Pid: %d", nGame.Slid, nGame.Pid))

    client := &http.Client{}
    req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventUrl, nGame.Slid, nGame.Pid), nil)

    // Установите заголовки
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
    req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")

    res, err := client.Do(req)
    if err != nil {
        DebugLog(fmt.Sprintf("Ошибка запроса: %v", err))
        return
    }
    defer res.Body.Close()

    if res.Body == nil {
        DebugLog(fmt.Sprintf("Не смогли получить данные по запросу о матче. Slid %d Pid %d", nGame.Slid, nGame.Pid))
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

    // Обработка данных матча
    var mapAllInterface []interface{}
    jsonErr := json.Unmarshal(resData, &mapAllInterface)
    if jsonErr != nil {
        DebugLog(fmt.Sprintf("Ошибка разбора JSON: %v", jsonErr))
        return
    }

    // Мы получили пустые данные. Такое бывает, если матч просрочен или данные не отдаются сервером, пропустим.
    if len(mapAllInterface) == 0 {
        DebugLog(fmt.Sprintf("Пустой ответ от сервера для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    nOneGame := nGame

    mapCoreItemInterface := mapAllInterface[0].(map[string]interface{})
    mapHInterface := mapCoreItemInterface["H"].(map[string]interface{})
    // Проверяем "LigaNaziv"
    if ligaNaziv, ok := mapHInterface["LigaNaziv"].(string); ok {
        nOneGame.LeagueName = ligaNaziv
    } else {
        DebugLog(fmt.Sprintf("Ошибка: LigaNaziv отсутствует или nil. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    // Проверяем "SLID"
    if slid, ok := mapHInterface["SLID"]; ok {
        nOneGame.Slid = InterfaceToInt64(slid)
    } else {
        DebugLog(fmt.Sprintf("Ошибка: SLID отсутствует или nil. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    // Проверяем "ParNaziv"
    if parNaziv, ok := mapHInterface["ParNaziv"].(string); ok {
        nOneGame.MatchName = parNaziv
    } else {
        DebugLog(fmt.Sprintf("Ошибка: ParNaziv отсутствует или nil. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    nOneGame.Source = "Sansabet"
    nOneGame.LeagueName = mapHInterface["LigaNaziv"].(string)
    nOneGame.Slid = InterfaceToInt64(mapHInterface["SLID"])
    nOneGame.MatchName = mapHInterface["ParNaziv"].(string)
    nOneGame.MatchId = strconv.FormatInt(nOneGame.Pid, 10)
    nOneGame.LeagueId = "0"

    ListGamesMux.Lock()
    ListGames[nGameKey] = nOneGame
    ListGamesMux.Unlock()

    // Если у нас не указан массив M в полученных данных - значит нет коэффициентов к событиям. Значит нам нечего дальше анализировать
    if mapCoreItemInterface["M"] == nil {
        DebugLog(fmt.Sprintf("Нет подмассива М в полученных данных. Не можем получить коэффициенты. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    mapKoeffInterface := mapCoreItemInterface["M"].([]interface{})
    Win1x2 := Win1x2Struct{Win1: 0, Win2: 0, WinNone: 0}
    Totals := make(map[string]WinLessMore)
    Handicap := make(map[string]WinHandicap)
    FirstTeamTotals := make(map[string]WinLessMore)
    SecondTeamTotals := make(map[string]WinLessMore)

    for _, nKoef := range mapKoeffInterface {
        nKoefMap := nKoef.(map[string]interface{})
        mapValBet, ok := nKoefMap["S"].([]interface{})
        if !ok {
            continue
        }

        for _, nValBet := range mapValBet {
            typeBet := int64(nValBet.(map[string]interface{})["N"].(float64))
            switch typeBet {
            // ПОБЕДА КОМАНДЫ
            case 1: // победа первой команды
                Win1x2.Win1 = nValBet.(map[string]interface{})["O"].(float64)
            case 2: // победа второй команды
                Win1x2.WinNone = nValBet.(map[string]interface{})["O"].(float64)
            case 10: // ничья между командами
                Win1x2.Win2 = nValBet.(map[string]interface{})["O"].(float64)

            // ТОТАЛЫ
            case 103: // тотал меньше чем указано в коэффициенте
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := Totals[detailBet]; !ok {
                    Totals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := Totals[detailBet]
                nK.WinLess = nValBet.(map[string]interface{})["O"].(float64)
                Totals[detailBet] = nK
            case 105: // тотал больше чем указано в коэффициенте
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := Totals[detailBet]; !ok {
                    Totals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := Totals[detailBet]
                nK.WinMore = nValBet.(map[string]interface{})["O"].(float64)
                Totals[detailBet] = nK

            // ИНДИВИДУАЛЬНЫЕ ТОТАЛЫ ПЕРВОЙ КОМАНДЫ
            case 169: // тотал меньше для первой команды
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := FirstTeamTotals[detailBet]; !ok {
                    FirstTeamTotals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := FirstTeamTotals[detailBet]
                nK.WinLess = nValBet.(map[string]interface{})["O"].(float64)
                FirstTeamTotals[detailBet] = nK
            case 168: // тотал больше для первой команды
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := FirstTeamTotals[detailBet]; !ok {
                    FirstTeamTotals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := FirstTeamTotals[detailBet]
                nK.WinMore = nValBet.(map[string]interface{})["O"].(float64)
                FirstTeamTotals[detailBet] = nK

            // ИНДИВИДУАЛЬНЫЕ ТОТАЛЫ ВТОРОЙ КОМАНДЫ
            case 171: // тотал меньше для второй команды
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := SecondTeamTotals[detailBet]; !ok {
                    SecondTeamTotals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := SecondTeamTotals[detailBet]
                nK.WinLess = nValBet.(map[string]interface{})["O"].(float64)
                SecondTeamTotals[detailBet] = nK
            case 170: // тотал больше для второй команды
                detailBet := ""
                if nKoefMap["B"] != nil {
                    detailBet = nKoefMap["B"].(string)
                }
                if _, ok := SecondTeamTotals[detailBet]; !ok {
                    SecondTeamTotals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                }
                nK := SecondTeamTotals[detailBet]
                nK.WinMore = nValBet.(map[string]interface{})["O"].(float64)
                SecondTeamTotals[detailBet] = nK

            // ГЕНДИКАПЫ (ФОРА)
            case 734: // {"t": "1 Ostatak"
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win1O = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            case 735: // {"t": "X Ostatak"
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.WinNoneO = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            case 736: // {"t": "2 Ostatak"
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win2O = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            case 737: // {"t": "1 Ost. I",
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win1 = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            case 738: // {"t": "X Ost. I",
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.WinNone = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            case 739: // {"t": "2 Ost. I",
                detailBet := ""
                if nKoefMap["R"] != nil {
                    detailBet = nKoefMap["R"].(string)
                }
                if _, ok := Handicap[detailBet]; !ok {
                    Handicap[detailBet] = WinHandicap{}
                }
                nK := Handicap[detailBet]
                nK.Win2 = nValBet.(map[string]interface{})["O"].(float64)
                Handicap[detailBet] = nK
            default:
                // Другие типы ставок
            }
        }
    }

    // Присвоение новых полей
    nOneGame.Win1x2 = Win1x2
    nOneGame.Totals = Totals
    nOneGame.Handicap = Handicap
    nOneGame.FirstTeamTotals = FirstTeamTotals   // Присвоение новых данных
    nOneGame.SecondTeamTotals = SecondTeamTotals // Присвоение новых данных

    // Преобразование в JSON
    jsonResult, err := json.MarshalIndent(nOneGame, "", "    ")
    if err != nil {
        DebugLog(fmt.Sprintf("Не смогли сформировать конечный json для вывода на экран для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    fmt.Println(string(jsonResult))

    // Отправляем данные в анализатор с использованием мьютекса для синхронизации
    analyzerConnMutex.Lock()
    err = analyzerConnection.WriteMessage(websocket.TextMessage, jsonResult)
    analyzerConnMutex.Unlock()
    if err != nil {
        log.Printf("Ошибка отправки сообщения: %v", err)
    }

    // Обновляем список игр
    ListGamesMux.Lock()
    ListGames[nGameKey] = nOneGame
    ListGamesMux.Unlock()
}

// Функция парсинга всех игр
func ParseGames() {
    DebugLog("Закидываем в потоки парсеры конкретных матчей")
    ListGamesMux.RLock()
    defer ListGamesMux.RUnlock()
    for nGameKey, nGame := range ListGames {
        go ParseOneGame(nGameKey, nGame)
    }
}

// Функция получения всех событий
func ParseEvents() {
    DebugLog("Начало получения данных о всех матчах")
    client := &http.Client{}
    req, _ := http.NewRequest("GET", fmt.Sprintf(allEventUrl, SlidAll), nil)

    // Установите заголовки
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
    req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")

    res, err := client.Do(req)
    if err != nil {
        DebugLog(fmt.Sprintf("Ошибка запроса: %v", err))
        return
    }
    defer res.Body.Close()

    if res.Body == nil {
        DebugLog("Не смогли получить ответ от сервера для всех матчах")
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

    var mapAllInterface []interface{}
    jsonErr := json.Unmarshal(resData, &mapAllInterface)
    if jsonErr != nil {
        DebugLog(fmt.Sprintf("Не смогли разложить json в объект для ответа о всех матчах"))
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

        // Фильтруем только по футболу
        if hMap["S"].(string) != "F" {
            continue
        }

        // Из функции periodTextDesktop понимаем, что время для футбола должно лежать по адресу p->t->m
        pMap := allInterfaceMap["P"].(map[string]interface{})
        tMap := pMap["T"].(map[string]interface{})
        nTime := int64(-2)
        if tMap["M"] != nil {
            nTime = StringToInt64(tMap["M"].(string))
        }

        // Фильтрация лайва: если время матча больше нуля, но меньше 90 - это лайв
        if nTime <= 0 || nTime >= 90 {
            continue
        }

        ListGamesMux.Lock()
        if _, ok := ListGames[nPid]; !ok {
            // Добавляем все футбольные матчи с указанием MatchId = Pid
            ListGames[nPid] = OneGame{
                Pid:     nPid,
                Slid:    0,
                MatchId: strconv.FormatInt(nPid, 10), // Присваиваем MatchId равным Pid
            }
        }
        ListGamesMux.Unlock()

    }
    DebugLog(fmt.Sprintf("Получение данных о всех матчах закончено. Новый ключ получения: %d Сейчас в обработке: %d", SlidAll, len(ListGames)))
}

// Запуск парсинга событий
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

// Функция подключения к анализатору
func connectToAnalyzer() {
    for {
        var err error
        analyzerConnection, _, err = websocket.DefaultDialer.Dial("ws://localhost:7100", nil)
        if err != nil {
            log.Printf("[ERROR] Ошибка подключения к анализатору: %v", err)
            time.Sleep(5 * time.Second)
            continue
        }
        log.Printf("[DEBUG] Подключение к анализатору установлено")
        break
    }
}

// Основная функция
func main() {
    DebugLog("Старт скрипта")
    ListGames = make(map[int64]OneGame)

    // Подключаемся к анализатору
    connectToAnalyzer()
    defer analyzerConnection.Close()

    // Запускаем парсинг в горутинах
    go ParseEventsStart()
    go ParseGamesStart()

    // Основной цикл
    select {}
}
