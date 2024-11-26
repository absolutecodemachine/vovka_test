<?php
error_reporting(E_ALL);
ini_set('display_errors', 1);

class Matching {
    private $Mysql;

    public function __construct(){
        $this->Mysql = new PDO(
            'mysql:host=localhost;dbname=matchingTeams;charset=utf8mb4',
            'matchingTeams',
            'local_password',
            [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]
        );
    }

    public function GetAllLeagues(){
        $data = [];
        
        // Получаем лиги Sansabet с информацией о спорте
        $query = "
            SELECT sl.*, s.name as sport_name 
            FROM sansabet_leagues sl
            JOIN sports s ON sl.sport_id = s.id
            ORDER BY s.name, sl.name";
        $res = $this->Mysql->query($query);
        $data['sansabet'] = $res->fetchAll(PDO::FETCH_ASSOC);
        
        // Получаем лиги Pinnacle с информацией о спорте
        $query = "
            SELECT pl.*, s.name as sport_name 
            FROM pinnacle_leagues pl
            JOIN sports s ON pl.sport_id = s.id
            ORDER BY s.name, pl.name";
        $res = $this->Mysql->query($query);
        $data['pinnacle'] = $res->fetchAll(PDO::FETCH_ASSOC);

        return $data;
    }

    public function CreateLeagueMatch($pinnacleLeagueId, $sansabetLeagueId){
        $query = "
            INSERT INTO league_matches (pinnacle_league_id, sansabet_league_id)
            VALUES (:pinnacle_id, :sansabet_id)";
        $stmt = $this->Mysql->prepare($query);
        return $stmt->execute([
            ':pinnacle_id' => $pinnacleLeagueId,
            ':sansabet_id' => $sansabetLeagueId
        ]);
    }

    public function GetLeaguePairs(){
        $query = "
            SELECT 
                lm.*,
                sl.name as sansabet_league_name,
                ss.name as sansabet_sport_name,
                pl.name as pinnacle_league_name,
                ps.name as pinnacle_sport_name
            FROM league_matches lm
            JOIN sansabet_leagues sl ON lm.sansabet_league_id = sl.id
            JOIN sports ss ON sl.sport_id = ss.id
            JOIN pinnacle_leagues pl ON lm.pinnacle_league_id = pl.id
            JOIN sports ps ON pl.sport_id = ps.id
            ORDER BY ss.name, sl.name";
        $res = $this->Mysql->query($query);
        return $res->fetchAll(PDO::FETCH_ASSOC);
    }

    public function GetUnmatchedTeams($sansabetLeagueId, $pinnacleLeagueId){
        $data = [];
        
        // Получаем непарные команды Sansabet
        $query = "
            SELECT st.* 
            FROM sansabet_teams st
            WHERE st.league_id = :league_id 
            AND st.pinnacle_team_id IS NULL
            ORDER BY st.name";
        $stmt = $this->Mysql->prepare($query);
        $stmt->execute([':league_id' => $sansabetLeagueId]);
        $data['sansabet'] = $stmt->fetchAll(PDO::FETCH_ASSOC);

        // Получаем непарные команды Pinnacle
        $query = "
            SELECT pt.* 
            FROM pinnacle_teams pt
            WHERE pt.league_id = :league_id
            AND NOT EXISTS (
                SELECT 1 FROM sansabet_teams st 
                WHERE st.pinnacle_team_id = pt.id
            )
            ORDER BY pt.name";
        $stmt = $this->Mysql->prepare($query);
        $stmt->execute([':league_id' => $pinnacleLeagueId]);
        $data['pinnacle'] = $stmt->fetchAll(PDO::FETCH_ASSOC);

        return $data;
    }

    public function CreateTeamMatch($pinnacleTeamId, $sansabetTeamId){
        $query = "
            UPDATE sansabet_teams 
            SET pinnacle_team_id = :pinnacle_id
            WHERE id = :sansabet_id";
        $stmt = $this->Mysql->prepare($query);
        return $stmt->execute([
            ':pinnacle_id' => $pinnacleTeamId,
            ':sansabet_id' => $sansabetTeamId
        ]);
    }
}

// Инициализация
$matching = new Matching;
$mode = $_GET['mode'] ?? 'league';

// Обработка создания пары лиг
if (isset($_GET['create_league_pair'])) {
    $pinnacleId = (int)$_GET['pinnacle_league_id'];
    $sansabetId = (int)$_GET['sansabet_league_id'];
    if ($pinnacleId && $sansabetId) {
        $matching->CreateLeagueMatch($pinnacleId, $sansabetId);
    }
}

// Обработка создания пары команд
if (isset($_GET['create_team_pair'])) {
    $pinnacleId = (int)$_GET['pinnacle_team_id'];
    $sansabetId = (int)$_GET['sansabet_team_id'];
    if ($pinnacleId && $sansabetId) {
        $matching->CreateTeamMatch($pinnacleId, $sansabetId);
    }
}

// Получение данных
$leagueData = $matching->GetAllLeagues();
$leaguePairs = $matching->GetLeaguePairs();

// Получение команд только если выбрана пара лиг
$teamData = [];
if (isset($_GET['league_pair']) && !empty($_GET['league_pair'])) {
    $pair = explode(',', $_GET['league_pair']);
    if (count($pair) === 2) {
        $sansabetLeagueId = (int)$pair[0];
        $pinnacleLeagueId = (int)$pair[1];
        if ($sansabetLeagueId && $pinnacleLeagueId) {
            $teamData = $matching->GetUnmatchedTeams($sansabetLeagueId, $pinnacleLeagueId);
        }
    }
}
?>

<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Manual Matching</title>
    <style>
        body {
            font-family: Arial, sans-serif;
            margin: 0;
            padding: 20px;
            background: #f5f5f5;
        }
        .container {
            max-width: 1200px;
            margin: 0 auto;
            background: white;
            padding: 20px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        .tabs {
            margin-bottom: 20px;
            border-bottom: 2px solid #eee;
        }
        .tab {
            display: inline-block;
            padding: 10px 20px;
            cursor: pointer;
            border: none;
            background: none;
            font-size: 16px;
            color: #666;
        }
        .tab.active {
            color: #2196F3;
            border-bottom: 2px solid #2196F3;
            margin-bottom: -2px;
        }
        .content {
            margin-top: 20px;
        }
        .row {
            display: flex;
            gap: 20px;
            margin-bottom: 20px;
        }
        .column {
            flex: 1;
        }
        select {
            width: 100%;
            padding: 8px;
            border: 1px solid #ddd;
            border-radius: 4px;
            margin-bottom: 10px;
        }
        button {
            width: 100%;
            padding: 10px;
            background: #2196F3;
            color: white;
            border: none;
            border-radius: 4px;
            cursor: pointer;
        }
        button:hover {
            background: #1976D2;
        }
        .header {
            margin-bottom: 15px;
            color: #333;
        }
        optgroup {
            font-weight: bold;
            color: #666;
        }
        .info {
            background: #E3F2FD;
            padding: 10px;
            border-radius: 4px;
            margin-bottom: 20px;
            color: #1976D2;
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="tabs">
            <a href="?mode=league" class="tab <?php echo $mode === 'league' ? 'active' : ''; ?>">Сопоставление лиг</a>
            <a href="?mode=team" class="tab <?php echo $mode === 'team' ? 'active' : ''; ?>">Сопоставление команд</a>
        </div>

        <?php if ($mode === 'league'): ?>
            <div class="content">
                <div class="info">
                    Выберите лиги для создания пары. Одна лига может участвовать в нескольких парах.
                </div>
                <form method="GET">
                    <input type="hidden" name="mode" value="league">
                    <input type="hidden" name="create_league_pair" value="1">
                    <div class="row">
                        <div class="column">
                            <h3 class="header">Лиги Sansabet</h3>
                            <select name="sansabet_league_id" size="20" required>
                                <?php
                                $currentSport = '';
                                foreach ($leagueData['sansabet'] as $league) {
                                    if ($currentSport !== $league['sport_name']) {
                                        if ($currentSport !== '') echo '</optgroup>';
                                        $currentSport = $league['sport_name'];
                                        echo '<optgroup label="' . htmlspecialchars($currentSport) . '">';
                                    }
                                    echo '<option value="' . $league['id'] . '">' . 
                                         htmlspecialchars($league['name']) . '</option>';
                                }
                                if ($currentSport !== '') echo '</optgroup>';
                                ?>
                            </select>
                        </div>
                        <div class="column">
                            <h3 class="header">Лиги Pinnacle</h3>
                            <select name="pinnacle_league_id" size="20" required>
                                <?php
                                $currentSport = '';
                                foreach ($leagueData['pinnacle'] as $league) {
                                    if ($currentSport !== $league['sport_name']) {
                                        if ($currentSport !== '') echo '</optgroup>';
                                        $currentSport = $league['sport_name'];
                                        echo '<optgroup label="' . htmlspecialchars($currentSport) . '">';
                                    }
                                    echo '<option value="' . $league['id'] . '">' . 
                                         htmlspecialchars($league['name']) . '</option>';
                                }
                                if ($currentSport !== '') echo '</optgroup>';
                                ?>
                            </select>
                        </div>
                    </div>
                    <button type="submit">Создать пару лиг</button>
                </form>
            </div>
        <?php else: ?>
            <div class="content">
                <div class="info">
                    Сначала выберите пару лиг, затем выберите команды для сопоставления.
                </div>
                <form method="GET" action="" style="margin-bottom: 20px;">
                    <input type="hidden" name="mode" value="team">
                    <select name="league_pair" size="10" onchange="this.form.submit()">
                        <option value="">-- Выберите пару лиг --</option>
                        <?php foreach ($leaguePairs as $pair): ?>
                            <?php
                            $selected = isset($_GET['league_pair']) && 
                                      $_GET['league_pair'] === $pair['sansabet_league_id'] . ',' . $pair['pinnacle_league_id'] 
                                      ? 'selected' : '';
                            ?>
                            <option value="<?php echo $pair['sansabet_league_id'] . ',' . $pair['pinnacle_league_id']; ?>" 
                                    <?php echo $selected; ?>>
                                <?php echo htmlspecialchars($pair['sansabet_sport_name'] . ' - ' . 
                                                          $pair['sansabet_league_name'] . ' ↔ ' . 
                                                          $pair['pinnacle_league_name']); ?>
                            </option>
                        <?php endforeach; ?>
                    </select>
                </form>

                <?php if (!empty($teamData)): ?>
                    <form method="GET">
                        <input type="hidden" name="mode" value="team">
                        <input type="hidden" name="create_team_pair" value="1">
                        <input type="hidden" name="league_pair" value="<?php echo htmlspecialchars($_GET['league_pair']); ?>">
                        <div class="row">
                            <div class="column">
                                <h3 class="header">Команды Sansabet</h3>
                                <select name="sansabet_team_id" size="20" required>
                                    <?php foreach ($teamData['sansabet'] as $team): ?>
                                        <option value="<?php echo $team['id']; ?>">
                                            <?php echo htmlspecialchars($team['name']); ?>
                                        </option>
                                    <?php endforeach; ?>
                                </select>
                            </div>
                            <div class="column">
                                <h3 class="header">Команды Pinnacle</h3>
                                <select name="pinnacle_team_id" size="20" required>
                                    <?php foreach ($teamData['pinnacle'] as $team): ?>
                                        <option value="<?php echo $team['id']; ?>">
                                            <?php echo htmlspecialchars($team['name']); ?>
                                        </option>
                                    <?php endforeach; ?>
                                </select>
                            </div>
                        </div>
                        <button type="submit">Создать пару команд</button>
                    </form>
                <?php endif; ?>
            </div>
        <?php endif; ?>
    </div>
</body>
</html>
