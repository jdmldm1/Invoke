package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleHistory reads the local PSReadLine history file and returns a list of unique commands.
func handleHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	historyList := []string{}
	
	// Read PSReadLine history file on Windows
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

	// Fallback to our config history if empty or not on Windows
	if len(historyList) == 0 {
		config := loadConfig()
		historyList = config.History
	}

	// Filter out duplicates and reverse (so newest are first)
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
