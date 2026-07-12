package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	historyList := []string{}

	appData := os.Getenv("APPDATA")
	if appData != "" {
		historyPath := filepath.Join(appData, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")
		if file, err := os.Open(historyPath); err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				cmd := strings.TrimSpace(scanner.Text())
				if cmd != "" {
					historyList = append(historyList, cmd)
				}
			}
		}
	}

	if len(historyList) == 0 {
		config := loadConfig()
		historyList = config.History
	}

	unique := []string{}
	seen := make(map[string]bool)
	for i := len(historyList) - 1; i >= 0; i-- {
		cmd := historyList[i]
		if !seen[cmd] {
			seen[cmd] = true
			unique = append(unique, cmd)
		}
		if len(unique) >= 150 {
			break
		}
	}

	json.NewEncoder(w).Encode(unique)
}
