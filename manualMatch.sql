create database matchingTeams;
use matchingTeams;

CREATE TABLE IF NOT EXISTS pinnacleTeams (
  id         BIGINT NOT NULL AUTO_INCREMENT,
  leagueName VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  sportName  VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  teamName   VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  
  PRIMARY KEY (id)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS sansabetTeams (
  id         BIGINT NOT NULL AUTO_INCREMENT,
  leagueName VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  sportName  VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  teamName   VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL default '',
  
  pinnacleId BIGINT NOT NULL default 0,

  PRIMARY KEY (id)
) ENGINE=InnoDB;

CREATE UNIQUE INDEX pinnacleTeams_uniq   ON pinnacleTeams(leagueName, sportName, teamName);
CREATE UNIQUE INDEX sansabetTeams_uniq   ON sansabetTeams(leagueName, sportName, teamName);

CREATE USER 'matchingTeams'@'localhost' IDENTIFIED BY 'local_password';
GRANT ALL ON matchingTeams.* TO 'matchingTeams'@'localhost';