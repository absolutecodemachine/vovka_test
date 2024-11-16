import asyncio
import json
from datetime import datetime
from aiohttp import ClientSession, BasicAuth

# Конфигурация
PINNACLE_API_URL = "https://api.pinnacle.com/"
PINNACLE_USERNAME = "AG1677099"
PINNACLE_PASSWORD = "5421123A"
SPORT_ID = 29  # ID футбола в Pinnacle API
LIVE_MODE = 1  # Только лайв
ODDS_FORMAT = "Decimal"
REQUEST_TIMEOUT = 5
PROXY = "http://AllivanService:PinnacleProxy@154.7.188.227:5242"

# Глобальное хранилище данных о матчах
matches_data = {}

# Класс для работы с Pinnacle API
class PinnacleAPI:
    def __init__(self, username, password, proxy=None):
        self.username = username
        self.password = password
        self.proxy = proxy

    def _url(self, endpoint, params=None):
        url = f"{PINNACLE_API_URL}{endpoint}?"
        if params:
            url += "&".join(f"{k}={v}" for k, v in params.items() if v is not None)
        return url

    async def _query(self, url):
        try:
            async with ClientSession() as session:
                async with session.get(
                    url,
                    timeout=REQUEST_TIMEOUT,
                    proxy=self._get_proxy(),
                    auth=self._get_auth(),
                    headers=self._get_headers()
                ) as response:
                    print(f"[LOG] Запрос к URL: {url}")
                    print(f"[LOG] Статус ответа: {response.status}")
                    result = await response.text()
                    return json.loads(result)
        except Exception as e:
            print(f"[ERROR] Ошибка запроса: {e}")
            return None

    def _get_auth(self):
        return BasicAuth(self.username, self.password)

    def _get_headers(self):
        return {"Accept": "application/json", "Content-Type": "application/json"}

    def _get_proxy(self):
        return self.proxy


# Функция для обновления данных о матчах
async def fetch_matches(api: PinnacleAPI):
    global matches_data
    url = api._url("v1/fixtures", {
        "sportId": SPORT_ID,
        "isLive": LIVE_MODE
    })
    data = await api._query(url)

    if not data or "league" not in data:
        print("[ERROR] Не удалось загрузить данные о матчах.")
        return

    matches_data.clear()
    for league in data["league"]:
        league_name = league.get("name", "Неизвестная лига")
        for event in league.get("events", []):
            if event.get("liveStatus", 0) == 1:
                event_id = event["id"]
                matches_data[event_id] = {
                    "home": event.get("home", "Неизвестная команда"),
                    "away": event.get("away", "Неизвестная команда"),
                    "league": league_name,
                    "start_time": event.get("starts"),
                }
    #print("[DEBUG] matches_data после обновления:", json.dumps(matches_data, indent=2, ensure_ascii=False))


# Функция для обработки данных события
def process_match_data(event_data):
    try:
        event_id = event_data["id"]
        if event_id in matches_data:
            home_team = matches_data[event_id]["home"]
            away_team = matches_data[event_id]["away"]
            league_name = matches_data[event_id]["league"]
            start_time = matches_data[event_id]["start_time"]
        else:
            print(f"[WARNING] Событие {event_id} отсутствует в matches_data.")
            home_team = "Неизвестная команда"
            away_team = "Неизвестная команда"
            league_name = "Неизвестная лига"
            start_time = None

        if start_time:
            start_time = datetime.fromisoformat(start_time.replace("Z", "+00:00"))

        outcomes = []
        for period in event_data.get("periods", []):
            if period.get("status") != 1:
                continue

            moneyline = period.get("moneyline", {})
            if moneyline:
                outcomes.append({"market": "Победа", "team": "Home", "odds": moneyline.get("home")})
                outcomes.append({"market": "Победа", "team": "Draw", "odds": moneyline.get("draw")})
                outcomes.append({"market": "Победа", "team": "Away", "odds": moneyline.get("away")})

            totals = period.get("totals", [])
            for total in totals:
                outcomes.append({
                    "market": "Общий тотал",
                    "line": total.get("points"),
                    "over_odds": total.get("over"),
                    "under_odds": total.get("under"),
                })

            spreads = period.get("spreads", [])
            for spread in spreads:
                outcomes.append({
                    "market": "Гандикап",
                    "line": spread.get("hdp"),
                    "home_odds": spread.get("home"),
                    "away_odds": spread.get("away"),
                })

            team_totals = period.get("teamTotal", {})
            for team, total in team_totals.items():
                if not total:
                    continue
                team_name = "Home" if team == "home" else "Away"
                outcomes.append({
                    "market": "Индивидуальный тотал",
                    "team": team_name,
                    "line": total.get("points"),
                    "over_odds": total.get("over"),
                    "under_odds": total.get("under"),
                })

        processed_event = {
            "event_id": event_id,
            "match_name": f"{home_team} vs {away_team}",
            "league_name": league_name,
            "start_time": start_time.isoformat() if start_time else "Неизвестно",
            "home_team": home_team,
            "away_team": away_team,
            "outcomes": outcomes,
        }
        print("[DEBUG] Обработанные данные события:", json.dumps(processed_event, indent=2, ensure_ascii=False))
        return processed_event
    except Exception as e:
        print(f"[ERROR] Ошибка в process_match_data: {e}")
        print(f"Данные события: {json.dumps(event_data, indent=2, ensure_ascii=False)}")
        return None


# Функция для получения коэффициентов
async def get_live_odds(api: PinnacleAPI):
    url = api._url("v1/odds", {
        "sportId": SPORT_ID,
        "isLive": LIVE_MODE,
        "oddsFormat": ODDS_FORMAT
    })
    data = await api._query(url)

    if not data:
        print("[ERROR] Ответ от API пустой или содержит ошибку.")
        return

    print("[LOG] Ответ от API успешно получен.")
    if "leagues" not in data:
        print("[ERROR] Ключ 'leagues' отсутствует в ответе.")
        print("Ответ:", json.dumps(data, indent=2, ensure_ascii=False))
        return

    for league in data["leagues"]:
        league_name = league.get("name", "Неизвестная лига")
        print(f"Лига: {league_name}")

        if "events" not in league:
            print(f"[WARNING] В лиге '{league_name}' отсутствуют события.")
            continue

        for event in league.get("events", []):
            processed_event = process_match_data(event)
            if not processed_event:
                print("[WARNING] Не удалось обработать событие.")
                continue

            print(f"Матч: {processed_event['match_name']}")
            print(f"Время начала: {processed_event['start_time']}")
            for outcome in processed_event["outcomes"]:
                print(outcome)
            print("-" * 50)


# Основной цикл
async def main():
    api = PinnacleAPI(PINNACLE_USERNAME, PINNACLE_PASSWORD, PROXY)
    while True:
        try:
            await fetch_matches(api)
            await get_live_odds(api)
        except Exception as e:
            print(f"[ERROR] Ошибка в основном цикле: {e}")
        await asyncio.sleep(5)


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("[LOG] Работа программы завершена пользователем.")
    except Exception as e:
        print(f"[ERROR] Ошибка в программе: {e}")
