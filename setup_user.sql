CREATE USER IF NOT EXISTS 'matchingTeams'@'localhost' IDENTIFIED BY 'local_password';
GRANT ALL PRIVILEGES ON matchingTeams.* TO 'matchingTeams'@'localhost';
FLUSH PRIVILEGES;
