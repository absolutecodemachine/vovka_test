package main
//это версия для добавления новых исходов, тут нужно заменить 2884 по всему коду 
//на номер матча в сансе который вы хотите залогировать (футбольный).
//Он сохранит все цены и выключит, цены будут в джсоне. 
//Затем вы находите коды нужных исходов и добавляете их в структуру, такие дела

import(
    "fmt"
    "net/http"
    "io/ioutil"
    "compress/gzip"
    "bytes"
    //"io"
    "encoding/json"
    "os"
    "time"
    "strconv"
    "sync"
    "github.com/gorilla/websocket"
    "log"
)

type OneGame struct{
    Name       string                  `json:"Name"`
    Pid        int64                   `json:"Pid"`
    Slid       int64                   `json:"Slid"`
    LeagueName string                  `json:"LeagueName"`
    MatchName  string                  `json:"MatchName"`
    MatchId    string                  `json:"MatchId"`
    LeagueId   string                  `json:"LeagueId"`
    Win1x2     Win1x2Struct            `json:"Win1x2"`
    Totals     map[string]WinLessMore  `json:"Totals"`
    Handicap   map[string]WinHandicap  `json:"Handicap"`
}

type WinLessMore struct{
    WinMore  float64   `json:"WinMore"`
    WinLess  float64   `json:"WinLess"`
}

type WinHandicap struct{
    Win1O       float64   `json:"Win1O"`
    WinNoneO    float64   `json:"WinNoneO"`
    Win2O       float64   `json:"Win2O"`
    Win1        float64   `json:"Win1"`
    WinNone     float64   `json:"WinNone"`
    Win2        float64   `json:"Win2"`
}

type Win1x2Struct struct{
    Win1     float64    `json:"Win1"`
    WinNone  float64    `json:"WinNone"`
    Win2     float64    `json:"Win2"`
}

type RequestGame struct{
    Slid string
    Parid string
}

const allEventUrl = "https://apilive.sansabet.com/api/LiveOdds/GetAll?SLID=%d"
const matchEventUrl = "https://apilive.sansabet.com/api/LiveOdds/GetByParIDs?SLID=%d&ParIDs=%d"
var SlidAll = int64(0)
var ListGames map[int64] OneGame
var ListGamesMutex sync.RWMutex

// Флаг и мьютекс для логирования
var hasLogged bool = false
var logMutex sync.Mutex

// создаем апдейтер для веб-соединения превращающего его в сокет.
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool {
        return true // Пропускаем любой запрос
    },
}
type Server struct {
    clients       map[*websocket.Conn]bool // простая карта для хранения подключенных клиентов.
    handleMessage func(message []byte) // хандлер новых сообщений
}

var server *Server

func InterfaceToInt64(inVal interface{}) int64{
    resInt, ok := inVal.(float64)
    if ok{
        return int64(resInt)
    }
    return -1
}

func StringToInt64(inVal string) int64{
    i, err := strconv.ParseInt(inVal, 10, 64)
    if  (err == nil){
        return i
    }
    return -1
}

func ParseOneGame(nGameKey int64, nGame OneGame){
    DebugLog(fmt.Sprintf("Начало анализа Slid: %d Pid: %d", nGame.Slid, nGame.Pid))

    client := &http.Client{}
    req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventUrl, nGame.Slid, nGame.Pid), nil)

    // Установите заголовки...
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

    if res.Body == nil{
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

    // Проверяем и логируем матч с Sifra = 2884
    logMutex.Lock()
    if !hasLogged {
        // Проверяем, что это футбольный матч и содержит данные
        var mapAllInterface []interface{}
        jsonErr := json.Unmarshal(resData, &mapAllInterface)
        if jsonErr != nil {
            DebugLog(fmt.Sprintf("Ошибка разбора JSON: %v", jsonErr))
            logMutex.Unlock()
            return
        }

        if len(mapAllInterface) > 0 {
            // Проверяем значение Sifra
            mapCoreItemInterface := mapAllInterface[0].(map[string]interface{})
            hMap := mapCoreItemInterface["H"].(map[string]interface{})
            sifra := InterfaceToInt64(hMap["Sifra"])
            if sifra == 2884 {
                // Логируем необработанные данные
                err := ioutil.WriteFile("raw_price_log.json", resData, 0644)
                if err != nil {
                    DebugLog("Ошибка записи в файл: " + err.Error())
                } else {
                    DebugLog("Необработанные данные матча с Sifra=2884 записаны в raw_price_log.json")
                    hasLogged = true
                    logMutex.Unlock()
                    os.Exit(0) // Завершаем программу
                }
            }
        }
    }
    logMutex.Unlock()

    // Остальная часть вашего кода для обработки матча...

    var mapAllInterface []interface{}
    jsonErr := json.Unmarshal(resData, &mapAllInterface)
    if jsonErr != nil {
        DebugLog(fmt.Sprintf("Ошибка разбора JSON: %v", jsonErr))
        return
    }

    // мы получили пустые данные. такое бывает если матч просрочен или данные не отдаются сервером пропустим.
    if (len(mapAllInterface) == 0){
        DebugLog(fmt.Sprintf("Пустой ответ от сервера для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return;
    }

    nOneGame := nGame

    mapCoreItemInterface := mapAllInterface[0].(map[string]interface{})
    mapHInterface := mapCoreItemInterface["H"].(map[string]interface{})

    nOneGame.LeagueName = mapHInterface["LigaNaziv"].(string)
    nOneGame.Slid       = InterfaceToInt64(mapHInterface["SLID"])
    nOneGame.MatchName  = mapHInterface["ParNaziv"].(string)
    nOneGame.MatchId    = "0"
    nOneGame.LeagueId   = "0"

    ListGamesMutex.Lock()
    ListGames[nGameKey] = nOneGame
    ListGamesMutex.Unlock()

    // если у нас не указан массив M в полученных данных - значит нет коэффициентов к событиям. Значит нам нечего дальше анализировать
    if (mapCoreItemInterface["M"] == nil){
        DebugLog(fmt.Sprintf("Нет подмассива М в полученных данных. Не можем получить коэффициенты. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    mapKoeffInterface := mapCoreItemInterface["M"].([]interface{})
    Win1x2   := Win1x2Struct{Win1: 0, Win2: 0, WinNone: 0}
    Totals   := make (map[string]WinLessMore)
    Handicap := make (map[string]WinHandicap)
    
    for _, nKoef := range mapKoeffInterface {			
        nKoefMap   := nKoef.(map[string] interface{})
        mapValBet  := nKoefMap["S"].([]interface{})

        for _, nValBet := range mapValBet {
            typeBet := int64(( nValBet.(map[string]interface {})["N"]).(float64))
            switch typeBet {
                // ПОБЕДА КОМАНДЫ
                case 1:             // победа первой команды
                    Win1x2.Win1 =  nValBet.(map[string]interface {})["O"].(float64)
                    break
                case 2: // победа второй команды
                    Win1x2.WinNone = nValBet.(map[string]interface{})["O"].(float64)
                    break
                case 10: // ничья между командами
                    Win1x2.Win2 = nValBet.(map[string]interface{})["O"].(float64)
                    break

                // ТОТАЛЫ
                case 103:           // тотал меньше чем указано в коэффициенте
                    detailBet := ""
                    if (nKoefMap["B"] != nil){
                        detailBet = nKoefMap["B"].(string)
                    }
                    if _, ok := Totals[detailBet]; !ok {
                        Totals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                    }
                    nK := Totals[detailBet]
                    nK.WinLess = nValBet.(map[string]interface {})["O"].(float64)
                    Totals[detailBet] = nK
                    break
                case 105:           // тотал больше чем указано в коэффициенте
                    detailBet := ""
                    if (nKoefMap["B"] != nil){
                        detailBet = nKoefMap["B"].(string)
                    }
                    if _, ok := Totals[detailBet]; !ok {
                        Totals[detailBet] = WinLessMore{WinLess: 0, WinMore: 0}
                    }
                    nK := Totals[detailBet]
                    nK.WinMore = nValBet.(map[string]interface {})["O"].(float64)
                    Totals[detailBet] = nK
                    break

                // ГЕНДИКАПЫ (ФОРА)
                case 734: //{"t": "1 Ostatak"
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.Win1O = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break

                case 735: //{"t": "X Ostatak"
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.WinNoneO = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break
                case 736: //{"t": "2 Ostatak"
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.Win2O = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break
                case 737: //{"t": "1 Ost. I",
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.Win1 = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break
                case 738: //{"t": "X Ost. I",
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.WinNone = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break
                case 739: //{"t": "2 Ost. I",
                    detailBet := ""
                    if (nKoefMap["R"] != nil){
                        detailBet = nKoefMap["R"].(string)
                    }
                    if _, ok := Handicap[detailBet]; !ok {
                        Handicap[detailBet] = WinHandicap{}
                    }
                    nK := Handicap[detailBet]
                    nK.Win2 = nValBet.(map[string]interface {})["O"].(float64)
                    Handicap[detailBet] = nK
                    break
                default:
                    break
            }
        }
    }

    nOneGame.Win1x2 = Win1x2
    nOneGame.Totals = Totals
    nOneGame.Handicap = Handicap
    
    jsonResult, err := json.MarshalIndent(nOneGame, "", "    ")
    // не смогли сформировать json - вернемся.
    if (err != nil){
        DebugLog(fmt.Sprintf("Не смогли сформировать конечный json для вывода на экран для матча. Slid %d Pid %d", nGame.Slid, nGame.Pid))
        return
    }

    fmt.Println(string(jsonResult))

    // отправляем данные в открытые сокеты.
    server.WriteMessage(jsonResult)

    ListGamesMutex.Lock()
    ListGames[nGameKey] = nOneGame
    ListGamesMutex.Unlock()
}

func ParseGames(){
    DebugLog(fmt.Sprintf("Закидываем в потоки парсеры конкретных матчей"))
    for nGameKey, nGame := range ListGames {
        go ParseOneGame(nGameKey, nGame)
    }
}

func ParseEvents(){
    DebugLog("начало получения данных о всех матчах")
    client := &http.Client{}
    req, _ := http.NewRequest("GET", fmt.Sprintf(allEventUrl, SlidAll) , nil)

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

    if res.Body == nil{
        DebugLog(fmt.Sprintf("Не смогли получить ответ от сервера для всех матчах"))
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
        sifra := InterfaceToInt64(hMap["Sifra"]) // Получаем значение Sifra

        if (nSlid > SlidAll){
            SlidAll = nSlid
        }
        
        // Фильтруем по футболу и по Sifra=2884
        if (hMap["S"].(string) != "F") || (sifra != 2884){
            continue
        }

        // из функции periodTextDesktop понимаем, что время для футбола должно лежать по адресу p->t->m
        pMap := allInterfaceMap["P"].(map[string]interface{})
        tMap := pMap["T"].(map[string]interface{})
        nTime := int64(-2)
        if (tMap["M"] != nil){
            nTime = StringToInt64(tMap["M"].(string))
        }

        // то, что я нашел по фильтрации лайва - если время матча больше нуля, но меньше 90 - это лайв
        if (nTime <= 0) || (nTime >= 90) {
            continue;
        }

        if _, ok := ListGames[nPid]; ok {
            // игра уже есть - ничего не делаем.
        } else {
            // игры нет - надо добавить в обработчик
            ListGamesMutex.Lock()
            ListGames[nPid] = OneGame{Pid: nPid, Slid : 0}
            ListGamesMutex.Unlock()
        }
    }
    DebugLog(fmt.Sprintf("Получение данных о всех матчах закончено. Новый ключ полуения: %d Сейчас в обработке: %d", SlidAll, len(ListGames)))

}

func DebugLog(str string){
    fmt.Println(str)
}

func ParseEventsStart(){
    for {
        ParseEvents()
        time.Sleep(5 * time.Second)
    }
}

func ParseGamesStart(){
    for {
        ParseGames()
        time.Sleep(5 * time.Second)
    }
}

func main (){
    DebugLog("Старт скрипта");
    ListGames = make(map[int64] OneGame)

    server = StartServer(messageHandler)

    // Запускаем парсинг в горутинах
    go ParseEventsStart()
    go ParseGamesStart()

    // Основной цикл
    for {
        server.WriteMessage([]byte("Bla-Bla"))
        time.Sleep(2000 * time.Millisecond)
    }
}

func messageHandler(message []byte) {
    fmt.Println(string(message))
}

func StartServer(handleMessage func(message []byte)) *Server {
    server := Server{
        make(map[*websocket.Conn]bool),
        handleMessage,
    }

    http.HandleFunc("/", server.echo)
    go http.ListenAndServe(":7100", nil) // Уводим http сервер в горутину 

    return &server
}

func (server *Server) echo(w http.ResponseWriter, r *http.Request) {
    connection, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("Ошибка обновления соединения:", err)
        return
    }
    defer connection.Close()

    server.clients[connection] = true // Сохраняем соединение, используя его как ключ
    defer delete(server.clients, connection) // Удаляем соединение

    for {
        mt, message, err := connection.ReadMessage()

        if err != nil || mt == websocket.CloseMessage {
            break // Выходим из цикла, если клиент пытается закрыть соединение или связь прервана
        }

        go server.handleMessage(message)
    }
}

func (server *Server) WriteMessage(message []byte) {
    for conn := range server.clients {
        err := conn.WriteMessage(websocket.TextMessage, message)
        if err != nil {
            log.Printf("Ошибка отправки сообщения: %v", err)
            conn.Close()
            delete(server.clients, conn)
        }
    }
}
