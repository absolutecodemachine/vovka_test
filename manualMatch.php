<?php
ini_set("display_errors","1");
ini_set("display_startup_errors","1");
ini_set('error_reporting', E_ALL);

class Matching 
{
	public $Mysql;


	public function __construct()
	{
		$hostName = 'localhost';
		$dbName   = 'matchingTeams';
		$login    = 'matchingTeams';
		$pwd      = 'local_password';
		$this->Mysql = new PDO('mysql:host=' . $hostName . ';dbname=' . $dbName, $login, $pwd);
	}

	public function CreateNewMatching($pinnacleId, $sansabetId){
		$pinnacleId = (int) $pinnacleId;
		$sansabetId = (int) $sansabetId;
		$query = "update sansabetTeams set pinnacleId = $sansabetId where id = $pinnacleId limit 1";
		$res = $this->Mysql->query($query);
		return true;
	}

	public function GetUnmachedTeams(){
		$data = [];
		
		$query = "select * from sansabetTeams where pinnacleId = 0 order by id DESC limit 50";
		$res = $this->Mysql->query($query);
		$data['sansabet'] = $res->fetchAll(PDO::FETCH_ASSOC);

		$query = "
			select sansabetTeams.pinnacleId as sid, pinnacleTeams.*
			from 
				pinnacleTeams 
			left join 
				sansabetTeams on (pinnacleTeams.id = sansabetTeams.pinnacleId)
			having 
				sansabetTeams.pinnacleId IS NULL
			order by 
				pinnacleTeams.id DESC 
			limit 50";
		$res = $this->Mysql->query($query);
		$data['pinnacle'] = $res->fetchAll(PDO::FETCH_ASSOC);

		return $data;
	}
}





$matching = new Matching;
$pinnacleId = (int) ($_GET['pinnacleId'] ?? $_POST['pinnacleId'] ?? 0);
$sansabetId = (int) ($_GET['sansabetId'] ?? $_POST['sansabetId'] ?? 0);
if (($pinnacleId > 0) && ($sansabetId > 0)){
	$matching->CreateNewMatching($pinnacleId, $sansabetId);
}

$data = $matching->GetUnmachedTeams();

?>


<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Ручной матчинг между shansbet и pinnacle</title>
	<style type="text/css">
		.row{
			width: 100%;
			min-width: 100%;
			max-width: 100%;
			display: block;
		}
		.col12{
			width: 100%;
			min-width: 100%;
			max-width: 100%;
			display: inline-block;
		}
		.col6{
			width: 40%;
			min-width: 40%;
			max-width: 40%;
			display: inline-block;
		}
		select {
			width: 100%;
			min-width: 100%;
			max-width: 100%;			
		}
		button{
			margin-top: 15px;
		}
	</style>	
</head>
<body>
	<div class="row">Выделите команду слева и команду справа. Потом нажать на кнопку "сохранить".</div>
	<form id="mForm" action="./manualMatch.php" method="GET"></form>
	<div class="row">
		<div class="col6">
			<div class="row"><h4>Pinnacle:</h4></div>
			<select form="mForm" name="pinnacleId" size="50">
				<?php foreach ($data['pinnacle'] as $value) {
					echo " <option value=\"$value[id]\">$value[teamName]</option> ";
				} ?>
			</select>			
		</div>
		<div class="col6">
			<div class="row"><h4>Sansabet:</h4></div>
			<select form="mForm" name="sansabetId" size="50">
				<?php foreach ($data['sansabet'] as $value) {
					echo " <option value=\"$value[id]\">$value[teamName]</option> ";
				} ?>
			</select>			
		</div>
	</div>
	<button form="mForm" type="submit">Сохранить</button>
</body>
</html>
