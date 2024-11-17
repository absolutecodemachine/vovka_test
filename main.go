package main

import (
    "log"
    "os"
    "os/exec"
    "path/filepath"
    "time"
)

func main() {
    // Убедимся, что папка logs существует
    if err := os.MkdirAll("logs", 0755); err != nil {
        log.Fatalf("Ошибка создания папки logs: %v", err)
    }

    log.Println("Запуск основного приложения")

    // Запуск парсеров
    go startProcess("go run parse_sansabet.go", "Sansabet Parser")
    go startProcess("go run parse_pinnacle.go", "Pinnacle Parser")

    // Запуск мэтчинга
    go startProcess("go run matching.go", "Matching")

    // Запуск анализатора
    go startProcess("go run analyzer.go", "Analyzer")

    // Основной цикл
    select {}
}

// Универсальная функция для запуска внешнего процесса с логами в файлы
func startProcess(command string, name string) {
    // Создаём путь для файла логов
    logFilePath := filepath.Join("logs", name+".log")

    // Открываем файл логов
    logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        log.Fatalf("Ошибка создания файла логов для %s: %v", name, err)
    }
    defer logFile.Close()

    for {
        log.Printf("Запуск процесса %s", name)
        cmd := exec.Command("sh", "-c", command) // Используем "sh -c", чтобы обрабатывать команды
        cmd.Stdout = logFile                    // Перенаправляем stdout в файл
        cmd.Stderr = logFile                    // Перенаправляем stderr в файл
        if err := cmd.Run(); err != nil {
            log.Printf("%s завершился с ошибкой: %v. Перезапуск через 5 секунд.", name, err)
        }
        time.Sleep(5 * time.Second) // Перезапуск через 5 секунд
    }
}
