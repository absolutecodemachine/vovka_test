-- Удаляем существующую базу данных если она есть
DROP DATABASE IF EXISTS matchingTeams;

-- Создаем базу данных заново
CREATE DATABASE matchingTeams;
USE matchingTeams;

-- Таблица видов спорта
CREATE TABLE IF NOT EXISTS sports (
    id         BIGINT NOT NULL AUTO_INCREMENT,
    name       VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB;

-- Таблица лиг Pinnacle
CREATE TABLE IF NOT EXISTS pinnacle_leagues (
    id         BIGINT NOT NULL AUTO_INCREMENT,
    sport_id   BIGINT NOT NULL,
    name       VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (sport_id) REFERENCES sports(id)
) ENGINE=InnoDB;

-- Таблица лиг Sansabet
CREATE TABLE IF NOT EXISTS sansabet_leagues (
    id         BIGINT NOT NULL AUTO_INCREMENT,
    sport_id   BIGINT NOT NULL,
    name       VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (sport_id) REFERENCES sports(id)
) ENGINE=InnoDB;

-- Таблица связей между лигами
CREATE TABLE IF NOT EXISTS league_matches (
    id                  BIGINT NOT NULL AUTO_INCREMENT,
    pinnacle_league_id  BIGINT NOT NULL,
    sansabet_league_id  BIGINT NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (pinnacle_league_id) REFERENCES pinnacle_leagues(id),
    FOREIGN KEY (sansabet_league_id) REFERENCES sansabet_leagues(id)
) ENGINE=InnoDB;

-- Таблица команд Pinnacle
CREATE TABLE IF NOT EXISTS pinnacle_teams (
    id         BIGINT NOT NULL AUTO_INCREMENT,
    sport_id   BIGINT NOT NULL,
    league_id  BIGINT NOT NULL,
    name       VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (sport_id) REFERENCES sports(id),
    FOREIGN KEY (league_id) REFERENCES pinnacle_leagues(id)
) ENGINE=InnoDB;

-- Таблица команд Sansabet
CREATE TABLE IF NOT EXISTS sansabet_teams (
    id               BIGINT NOT NULL AUTO_INCREMENT,
    sport_id         BIGINT NOT NULL,
    league_id        BIGINT NOT NULL,
    name             VARCHAR(200) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL,
    pinnacle_team_id BIGINT,
    PRIMARY KEY (id),
    FOREIGN KEY (sport_id) REFERENCES sports(id),
    FOREIGN KEY (league_id) REFERENCES sansabet_leagues(id),
    FOREIGN KEY (pinnacle_team_id) REFERENCES pinnacle_teams(id)
) ENGINE=InnoDB;

-- Создаем уникальные индексы
CREATE UNIQUE INDEX sports_uniq ON sports(name);
CREATE UNIQUE INDEX pinnacle_leagues_uniq ON pinnacle_leagues(sport_id, name);
CREATE UNIQUE INDEX sansabet_leagues_uniq ON sansabet_leagues(sport_id, name);
CREATE UNIQUE INDEX pinnacle_teams_uniq ON pinnacle_teams(sport_id, league_id, name);
CREATE UNIQUE INDEX sansabet_teams_uniq ON sansabet_teams(sport_id, league_id, name);

-- Создаем дополнительные индексы для оптимизации
CREATE INDEX league_matches_pinnacle ON league_matches(pinnacle_league_id);
CREATE INDEX league_matches_sansabet ON league_matches(sansabet_league_id);

-- Создаем пользователя и даем ему права
CREATE USER IF NOT EXISTS 'matchingTeams'@'localhost' IDENTIFIED BY 'local_password';
GRANT ALL ON matchingTeams.* TO 'matchingTeams'@'localhost';
