package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Snippet struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

type PromptTemplate struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

type ConfigData struct {
	OllamaHost  string            `json:"ollama_host"`
	OllamaModel string            `json:"ollama_model"`
	History     []string          `json:"history"`
	Snippets    []Snippet         `json:"snippets"`
	Prompts     []PromptTemplate  `json:"prompts"`
	Aliases     map[string]string `json:"aliases"`
	JarvisPath  string            `json:"jarvis_path"`
}

var (
	configPath string
	configMu   sync.Mutex
)

func initConfig() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	configPath = filepath.Join(home, ".powerterm.json")
}

func loadConfig() ConfigData {
	configMu.Lock()
	defer configMu.Unlock()

	// Default config
	defaultConfig := ConfigData{
		OllamaHost:  "http://shiloh:11434",
		OllamaModel: "gemma4:e4b",
		JarvisPath:  "",
		History:     []string{},
		Snippets: []Snippet{
			{Name: "Git Status", Cmd: "git status"},
			{Name: "Git Commit & Push", Cmd: "git commit -m \"update\"; git push"},
			{Name: "Git: Commit with message", Cmd: "git add . && git commit -m \"{message}\" && git push"},
			{Name: "Get Network IPs", Cmd: "Get-NetIPAddress | Format-Table IPAddress, InterfaceAlias"},
			{Name: "List Active Ports", Cmd: "Get-NetTCPConnection | Where-Object {$_.State -eq \"Listen\"} | Select-Object LocalPort, OwningProcess"},
			{Name: "Top CPU Processes", Cmd: "Get-Process | Sort-Object CPU -Descending | Select-Object -First 10"},
			{Name: "Docker: Containers Running", Cmd: "docker ps"},
			{Name: "Docker: Run image", Cmd: "docker run -d -p {host_port}:{container_port} --name {container_name} {image}"},
			{Name: "Docker: Exec into container", Cmd: "docker exec -it {container_name} /bin/bash"},
			{Name: "Docker: Clean unused", Cmd: "docker system prune -a --volumes"},
			{Name: "K8s: Get Pods", Cmd: "kubectl get pods -A"},
			{Name: "K8s: Logs", Cmd: "kubectl logs {pod} -n {namespace} --follow"},
			{Name: "K8s: Describe pod", Cmd: "kubectl describe pod {pod} -n {namespace}"},
			{Name: "SSH connect", Cmd: "ssh {user}@{host}"},
			{Name: "SCP upload", Cmd: "scp {local_path} {user}@{host}:{remote_path}"},
			{Name: "Go: Run current", Cmd: "go run ."},
			{Name: "Go: Build", Cmd: "go build -o {output} ."},
			{Name: "NPM: Run Dev", Cmd: "npm run dev"},
			{Name: "NPM: Install package", Cmd: "npm install {package}"},
			{Name: "Find in files", Cmd: "Get-ChildItem -Recurse | Select-String -Pattern \"{pattern}\""},
			{Name: "Kill port", Cmd: "Get-Process -Id (Get-NetTCPConnection -LocalPort {port}).OwningProcess | Stop-Process -Force"},
		},
		Prompts: []PromptTemplate{
			{Name: "Explain Regex", Template: "Explain this regular expression: {regex}"},
			{Name: "Refactor Code", Template: "Refactor this code to make it more concise, clean, and efficient:\n\n{code}"},
			{Name: "Unit Test Generator", Template: "Write comprehensive unit tests for this code block:\n\n{code}"},
			{Name: "Shell Helper", Template: "How do I run {action} in the PowerShell terminal?"},
		},
		Aliases: map[string]string{
			"gco":  "git checkout ",
			"gst":  "git status -sb",
			"gp":   "git pull",
			"gps":  "git push",
			"gl":   "git log --oneline --graph -15",
			"dps":  "docker ps",
			"drun": "docker run -d -p {host_port}:{container_port} ",
			"kgp":  "kubectl get pods",
			"ptp":  "Get-NetTCPConnection -State Listen",
		},
	}

	file, err := os.Open(configPath)
	if err != nil {
		return defaultConfig
	}
	defer file.Close()

	// To handle legacy configurations (where Bookmarks was []string),
	// we will decode into a generic interface first, then check.
	var raw map[string]interface{}
	_ = json.NewDecoder(file).Decode(&raw)

	// Build config from raw map
	var data ConfigData

	if raw != nil {
		if host, ok := raw["ollama_host"].(string); ok && host != "" {
			data.OllamaHost = host
		} else {
			data.OllamaHost = defaultConfig.OllamaHost
		}

		if model, ok := raw["ollama_model"].(string); ok && model != "" {
			data.OllamaModel = model
		} else {
			data.OllamaModel = defaultConfig.OllamaModel
		}



		// Handle history
		if histRaw, ok := raw["history"].([]interface{}); ok {
			for _, h := range histRaw {
				if hStr, ok := h.(string); ok {
					data.History = append(data.History, hStr)
				}
			}
		}

		// Handle snippets
		if snipRaw, ok := raw["snippets"].([]interface{}); ok {
			for _, s := range snipRaw {
				if sMap, ok := s.(map[string]interface{}); ok {
					name, _ := sMap["name"].(string)
					cmd, _ := sMap["cmd"].(string)
					if name != "" && cmd != "" {
						data.Snippets = append(data.Snippets, Snippet{Name: name, Cmd: cmd})
					}
				}
			}
		}
		if len(data.Snippets) == 0 {
			data.Snippets = defaultConfig.Snippets
		}
		// Handle prompts
		if promptRaw, ok := raw["prompts"].([]interface{}); ok {
			for _, p := range promptRaw {
				if pMap, ok := p.(map[string]interface{}); ok {
					name, _ := pMap["name"].(string)
					template, _ := pMap["template"].(string)
					if name != "" && template != "" {
						data.Prompts = append(data.Prompts, PromptTemplate{Name: name, Template: template})
					}
				}
			}
		}
		if len(data.Prompts) == 0 {
			data.Prompts = defaultConfig.Prompts
		}

		// Handle aliases
		data.Aliases = make(map[string]string)
		if aliasRaw, ok := raw["aliases"].(map[string]interface{}); ok {
			for k, v := range aliasRaw {
				if vStr, ok := v.(string); ok {
					data.Aliases[k] = vStr
				}
			}
		}
		if len(data.Aliases) == 0 {
			data.Aliases = defaultConfig.Aliases
		}

		// Handle jarvis_path
		if jarvis, ok := raw["jarvis_path"].(string); ok {
			data.JarvisPath = jarvis
		}
	} else {
		data = defaultConfig
	}

	return data
}

func saveConfig(data ConfigData) {
	configMu.Lock()
	defer configMu.Unlock()

	file, err := os.Create(configPath)
	if err != nil {
		log.Printf("Failed to create config file: %v", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(data)
}

func addSnippet(name, cmd string) {
	name = strings.TrimSpace(name)
	cmd = strings.TrimSpace(cmd)
	if name == "" || cmd == "" {
		return
	}
	config := loadConfig()
	config.Snippets = append(config.Snippets, Snippet{Name: name, Cmd: cmd})
	saveConfig(config)
}

func deleteSnippet(index int) {
	config := loadConfig()
	if index < 0 || index >= len(config.Snippets) {
		return
	}
	config.Snippets = append(config.Snippets[:index], config.Snippets[index+1:]...)
	saveConfig(config)
}

func addHistory(cmd string) {
	if cmd == "" {
		return
	}
	config := loadConfig()
	newHistory := []string{cmd}
	for _, h := range config.History {
		if h != cmd {
			newHistory = append(newHistory, h)
		}
	}
	if len(newHistory) > 200 {
		newHistory = newHistory[:200]
	}
	config.History = newHistory
	saveConfig(config)
}

func addPrompt(name, template string) {
	name = strings.TrimSpace(name)
	template = strings.TrimSpace(template)
	if name == "" || template == "" {
		return
	}
	config := loadConfig()
	config.Prompts = append(config.Prompts, PromptTemplate{Name: name, Template: template})
	saveConfig(config)
}

func deletePrompt(index int) {
	config := loadConfig()
	if index < 0 || index >= len(config.Prompts) {
		return
	}
	config.Prompts = append(config.Prompts[:index], config.Prompts[index+1:]...)
	saveConfig(config)
}
