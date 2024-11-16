package main

import(
	"fmt"
	"net/http"
	"io/ioutil"
	"compress/gzip"
	"bytes"
	"io"
	"encoding/json"
	"os"
	"time"
	"strconv"
	"sync"
	"github.com/gorilla/websocket"
	//"log"
)

type OneGame struct{
	Name       string                  `json:"Name"`       // 
	Pid        int64                   `json:"Pid"`        // идентификатор матча внутри системы (внутренний айди)
	Slid       int64                   `json:"Slid"`       // идентификатор обновления матча (при запросе используется как ключ)
	LeagueName string                  `json:"LeagueName"` // наименование лиги где идет матч
	MatchName  string                  `json:"MatchName"`  // наименование матча
	MatchId    string                  `json:"MatchId"`    // идентификатор матча
	LeagueId   string                  `json:"LeagueId"`   // идентификатор лиги
	Win1x2     Win1x2Struct            `json:"Win1x2"`     // ставка на выигрыш 1 или 2 команды.
	Totals     map[string]WinLessMore  `json:"Totals"`     // тоталы. ключ - коэффициент тотала. значение - структура 
	Handicap   map[string]WinHandicap  `json:"Handicap"`   // Handicap. ключ - коэффициент Handicap. значение - структура 

}

type WinLessMore struct{
	WinMore  float64   `json:"WinMore"`         // коэффициент на ставку больше
	WinLess  float64   `json:"WinLess"`         // коэффициент на ставку меньше
}
type WinHandicap struct{
	Win1O       float64   `json:"Win1O"`        // первые три это кэфы на остаток матча
	WinNoneO    float64   `json:"WinNoneO"`     // 
	Win2O       float64   `json:"Win2O"`        // 
	Win1        float64   `json:"Win1"`         // А эти три на остаток первого тайма
	WinNone     float64   `json:"WinNone"`      // 
	Win2        float64   `json:"Win2"`         // 
}
type Win1x2Struct struct{
	Win1     float64    `json:"Win1"`           // коэффициент на победу первой команды
	WinNone  float64    `json:"WinNone"`        // коэффициент на ничью
	Win2     float64    `json:"Win2"`           // коэффициент на победу второй команды
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
	resInt := float64(-1)
	resInt, ok := inVal.(float64)
	if (ok){
		return int64(resInt)
	}
	return int64(-1)
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
	req, _ := http.NewRequest("GET", fmt.Sprintf(matchEventUrl, nGame.Slid, nGame.Pid) , nil)

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

	// сервер нам может не вернуть них....я
	res, _ := client.Do(req)
	if (res.Body == nil){
		DebugLog(fmt.Sprintf("Не смогли получить данные по запросу о матче. Slid %d Pid %d", nGame.Slid, nGame.Pid))
		return
	}

	body, _ := ioutil.ReadAll(res.Body)
	
	b := bytes.NewBuffer(body)
	var r io.Reader
	r, _ = gzip.NewReader(b)

	var resB bytes.Buffer
	_, _ = resB.ReadFrom(r)
	resData := resB.Bytes()

	var mapAllInterface []interface{}
	jsonErr := json.Unmarshal(resData, &mapAllInterface)
    if jsonErr != nil {
        fmt.Println(jsonErr)
        os.Exit(5)
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
	// server.WriteMessage([]byte("Hello"))
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

	res, _ := client.Do(req)
	if (res.Body == nil){
		DebugLog(fmt.Sprintf("Не смогли получить ответ от сервера для всех матчах"))
		return
	}

	body, _ := ioutil.ReadAll(res.Body)
	
	b := bytes.NewBuffer(body)
	var r io.Reader
	r, _ = gzip.NewReader(b)

	var resB bytes.Buffer
	_, _ = resB.ReadFrom(r)
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

		if (nSlid > SlidAll){
			SlidAll = nSlid
		}
		
		// тип спорта лежит в H -> S. Фильтруем по футболу. 
		if (hMap["S"].(string) != "F"){
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
	for true {
		ParseEvents()
		time.Sleep(5 * time.Second)
	}
}

func ParseGamesStart(){
	for true {
		ParseGames()
		time.Sleep(5 * time.Second)
	}
}

func main (){
	DebugLog("Старт скрипта");
	ListGames = make(map[int64] OneGame)

	server = StartServer(messageHandler)

	// for {
	// 	server.WriteMessage([]byte("Hello"))
	// }

	go ParseEventsStart()
	go ParseGamesStart()

	for true {
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
	connection, _ := upgrader.Upgrade(w, r, nil)
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
		conn.WriteMessage(websocket.TextMessage, message)
	}
}







/*
// https://pc-d.statscore.com/get_pushes/5428215?messageId=1020315185&auth=d1bb04e7&poll=true
https://sansabet.com/JSONs/JsonIgriTipoviLive.aspx
1x2:
1 - победа первой, 2 - ничья, 10- победа второй
{
    "MS": "OPEN",
    "S": [
        {
            "N": 1,
            "O": 1.63,
            "P": null
        },
        {
            "N": 2,
            "O": 3.30,
            "P": null
        },
        {
            "N": 10,
            "O": 6.90,
            "P": null
        }
    ]
},


Тоталы:
записей с тотаблами может быть хоть 20 штук.
103 - меньше, чем указано в В, 105 - больше, чем указано в В
{
    "MS": "OPEN",
    "B": "0.5",
    "S": [
        {
            "N": 103,
            "O": 7.00,
            "P": null
        },
        {
            "N": 105,
            "O": 1.06,
            "P": null
        }
    ]
},



Гандикапы(фора):
на самом деле хер знает что там внутри. нужны именно эти коэффициенты
{
    "MS": "OPEN",
    "R": "0-1",
    "S": [
        {
            "N": 734,
            "O": 1.58,
            "P": null
        },
        {
            "N": 735,
            "O": 3.00,
            "P": null
        },
        {
            "N": 736,
            "O": 10.0,
            "P": null
        }
        ],

734: {
    "t": "1 Ostatak",
    "i": 94,
    "o": 7340,
    "inv": 0
},
735: {
    "t": "X Ostatak",
    "i": 94,
    "o": 7350,
    "inv": 0
},
736: {
    "t": "2 Ostatak",
    "i": 94,
    "o": 7360,
    "inv": 0
},
737: {
    "t": "1 Ost. I",
    "i": 95,
    "o": 7370,
    "inv": 0
},
738: {
    "t": "X Ost. I",
    "i": 95,
    "o": 7380,
    "inv": 0
},
739: {
    "t": "2 Ost. I",
    "i": 95,
    "o": 7390,
    "inv": 0
},




Тотал команды 1/2:

{
    "MS": "OPEN",
    "B": "1.5",
    "S": [
        {
            "N": 169,
            "O": 1.38,
            "P": null
        },
        {
            "N": 168,
            "O": 2.90,
            "P": null
        }
    ]
},

168: {
    "t": "T1 $+",
    "i": 59,
    "o": 5650,
    "inv": 0
},
169: {
    "t": "T1 $-",
    "i": 59,
    "o": 5640,
    "inv": 0
},
170: {
    "t": "T2 $+",
    "i": 60,
    "o": 5670,
    "inv": 0
},
171: {
    "t": "T2 $-",
    "i": 60,
    "o": 5660,
    "inv": 0
},
168 и 169 - тоталы для первой команды. 168 - больше, чем в В, 169 - меньше.



Статистика:


https://sansabet.com/JSONs/JsonSportoviTV.aspx
var SportoviNazivi={
    0: 'Fudbal',
    1: 'Futsal',
    3: 'Dueli',
    4: 'Ide Dalje',
    7: 'Rukomet',
    8: 'Hokej',
    9: 'Vaterpolo',
    10: 'Hendikep',
    11: 'Imaginaran',
    22: 'Košarka',
    25: 'Pobednik',
    26: 'Pobednik',
    27: 'Pobednik',
    28: '1-3 mesto',
    29: '1-6 mesto',
    37: 'Tenis',
    38: 'Odbojka',
    39: 'Bejzbol',
    40: 'Borilački sportovi',
    41: 'Američki fudbal',
    42: 'Ragbi',
    43: 'Snuker',
    44: 'Pikado',
    45: 'Stoni tenis',
    46: 'Fudbal na pesku',
    47: 'Moto Sport',
    48: 'Atletika',
    49: 'Specijalan singl',
    50: 'Ski sportovi',
    51: 'Posebne igre',
    52: 'eSport',
    53: 'FIFA',
    54: 'NBA2k',
    55: 'eTenis',
};var SportName={
    'F': 'Fudbal',
    'FU': 'Futsal',
    'H': 'Rukomet',
    'IH': 'Hokej',
    'WP': 'Vaterpolo',
    'B': 'Košarka',
    'T': 'Tenis',
    'V': 'Odbojka',
    'BB': 'Bejzbol',
    'AF': 'Američki fudbal',
    'S': 'Snuker',
    'TT': 'Stoni tenis',
    'EF': 'FIFA',
    'EB': 'NBA2k',
    'ET': 'eTenis',
};var SportPoint={
    'ORG': 'Poena',
    'F': 'golova',
    'FU': 'golova',
    'H': 'golova',
    'IH': 'golova',
    'WP': 'golova',
    'B': 'poena',
    'T': 'gemova',
    'V': 'poena',
    'BB': 'poena',
    'AF': 'golova',
    'S': 'poena',
    'TT': 'poena',
    'EF': 'golova',
    'EB': 'poena',
    'ET': 'points',
};var SportOrder={
    'F': '1',
    'FU': '53',
    'H': '9',
    'IH': '7',
    'WP': '13',
    'B': '3',
    'T': '5',
    'V': '11',
    'BB': '17',
    'AF': '15',
    'S': '23',
    'TT': '29',
    'EF': '74',
    'EB': '75',
    'ET': '76',
};


*/