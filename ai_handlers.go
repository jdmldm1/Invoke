package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ChatHistoryEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatState struct {
	History []ChatHistoryEntry `json:"history"`
	Paths   []string           `json:"paths"`
}

func handleAIComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Line string `json:"line"`
		CWD  string `json:"cwd"`
		OS   string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Line) == "" {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	cfg := loadConfig()
	osHint := "Windows PowerShell"
	if strings.ToLower(req.OS) == "linux" {
		osHint = "Linux bash"
	}

	systemPrompt := "You are a " + osHint + " shell autocomplete engine. " +
		"Complete the partial command the user is typing. " +
		"Reply with ONLY the completion suffix (the characters after the cursor), nothing else. " +
		"If you are unsure, reply with an empty string."

	userPrompt := "Current directory: " + req.CWD + "\nPartial command: " + req.Line

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream":      false,
		"num_predict": 40,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	completion := strings.TrimSpace(ollamaResp.Message.Content)
	completion = strings.TrimPrefix(completion, "`")
	completion = strings.TrimSuffix(completion, "`")
	if idx := strings.IndexByte(completion, '\n'); idx >= 0 {
		completion = completion[:idx]
	}

	json.NewEncoder(w).Encode(map[string]string{"completion": completion})
}

func handleAITranslate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Query string `json:"query"`
		CWD   string `json:"cwd"`
		OS    string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		http.Error(w, `{"error":"query, cwd and os required"}`, 400)
		return
	}

	cfg := loadConfig()
	osHint := "Windows PowerShell"
	if strings.ToLower(req.OS) == "linux" {
		osHint = "Linux bash"
	}

	systemPrompt := "You are a terminal command translator. Translate the user's natural language request into a single executable shell command " +
		"for " + osHint + " in the directory: " + req.CWD + ".\n" +
		"Output ONLY the raw command, without markdown code fences, without quotes, and without any explanation."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Query},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"command": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	cmd := strings.TrimSpace(ollamaResp.Message.Content)
	cmd = strings.TrimPrefix(cmd, "`")
	cmd = strings.TrimSuffix(cmd, "`")

	json.NewEncoder(w).Encode(map[string]string{"command": cmd})
}

func handleAIEditCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Code        string `json:"code"`
		Instruction string `json:"instruction"`
		Lang        string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.Instruction) == "" {
		http.Error(w, `{"error":"code and instruction required"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a professional code refactoring and generation tool. " +
		"Perform the user's edit instruction on the provided " + req.Lang + " code.\n" +
		"Output ONLY the corrected code block. Do NOT include markdown code fences, do NOT include explanations, and do NOT write preamble/postamble."

	userPrompt := "Code:\n" + req.Code + "\n\nInstruction:\n" + req.Instruction

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	output := strings.TrimSpace(ollamaResp.Message.Content)
	output = strings.TrimPrefix(output, "```"+req.Lang)
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	json.NewEncoder(w).Encode(map[string]string{"code": output})
}

func handleAIScaffoldScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
		Lang string `json:"lang"`
		Desc string `json:"desc"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Desc) == "" {
		http.Error(w, `{"error":"invalid request"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := fmt.Sprintf("You are an expert scripting assistant. Write a functional %s script to accomplish the user's task.\n"+
		"Output ONLY the raw script code, with no markdown code fences, no quotes, and no formatting explanations. Start directly with the code.", req.Lang)

	userPrompt := fmt.Sprintf("Script Name: %s\nRequirements: %s", req.Name, req.Desc)

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	output := strings.TrimSpace(ollamaResp.Message.Content)
	output = strings.TrimPrefix(output, "```powershell")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)

	json.NewEncoder(w).Encode(map[string]string{"code": output})
}

func handleAIExplainHover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Symbol  string `json:"symbol"`
		Line    string `json:"line"`
		Context string `json:"context"`
		Lang    string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Symbol == "" {
		http.Error(w, `{"error":"symbol required"}`, 400)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a code explainer tooltip generator. Briefly explain what the selected code keyword, symbol, or function does in 1-3 sentences.\n" +
		"Keep it clear and precise. Do not write complex markdown tags. Do not repeat the symbol name."

	userPrompt := fmt.Sprintf("Language: %s\nSymbol: %s\nLine context: %s\nCode context:\n%s", req.Lang, req.Symbol, req.Line, req.Context)

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"explanation": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	explanation := strings.TrimSpace(ollamaResp.Message.Content)
	json.NewEncoder(w).Encode(map[string]string{"explanation": explanation})
}

func handleAIEditorComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Before string `json:"before"`
		Lang   string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Before == "" {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a code completion model. Write the exact immediate next characters/lines of code that should continue from the user's cursor position.\n" +
		"Output ONLY the completion suffix, without markdown backticks, without formatting, and without any explanation."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Before},
		},
		"stream":      false,
		"num_predict": 60,
		"temperature": 0.2,
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(respBody, &ollamaResp)

	completion := strings.TrimSpace(ollamaResp.Message.Content)
	completion = strings.TrimPrefix(completion, "```"+req.Lang)
	completion = strings.TrimPrefix(completion, "```")
	completion = strings.TrimSuffix(completion, "```")
	completion = strings.TrimSpace(completion)

	json.NewEncoder(w).Encode(map[string]string{"completion": completion})
}

func handleAICLISchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Tool) == "" {
		http.Error(w, `{"error":"Invalid request payload"}`, http.StatusBadRequest)
		return
	}

	cfg := loadConfig()
	systemPrompt := "You are a CLI helper schema generator. Output ONLY a valid JSON array of objects representing the most common flags/parameters for the CLI tool '" + req.Tool + "'.\n" +
		"Each object MUST have the following structure:\n" +
		"- name: the flag string (e.g. '-i', '--port', or 'target' for a positional arg)\n" +
		"- type: 'boolean' (for flags without value), 'string' (for flags with text input), or 'choice' (for flags with pre-defined values)\n" +
		"- choices: (only for type 'choice') an array of strings representing options\n" +
		"- description: a very short (1 sentence) explanation of what it does\n" +
		"- default: (optional) default value\n" +
		"- placeholder: (optional) placeholder text\n" +
		"Output ONLY the raw JSON array. No explanations, no markdown block code fences, no markdown formatting."

	payload := map[string]any{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": "Generate common flags schema for: " + req.Tool},
		},
		"stream":      false,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"Failed to call AI: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var ollamaResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		http.Error(w, `{"error":"Failed to decode AI response"}`, http.StatusInternalServerError)
		return
	}

	content := strings.TrimSpace(ollamaResp.Message.Content)
	for _, prefix := range []string{"```json", "```javascript", "```"} {
		if strings.HasPrefix(content, prefix) {
			content = strings.TrimPrefix(content, prefix)
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
			break
		}
	}

	var testJSON []any
	if err := json.Unmarshal([]byte(content), &testJSON); err != nil {
		errResp, _ := json.Marshal(map[string]string{
			"error": "AI did not return valid JSON schema",
			"raw":   content,
		})
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(errResp)
		return
	}

	w.Write([]byte(content))
}

func handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	htmlBytes, err := webFS.ReadFile("web/chat.html")
	if err != nil {
		http.Error(w, "Chat template not found", 404)
		return
	}
	w.Write(htmlBytes)
}

func handleChatState(w http.ResponseWriter, r *http.Request) {
	session := r.URL.Query().Get("session")
	if session == "" {
		session = "default"
	}

	var safeSession strings.Builder
	for _, char := range session {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			safeSession.WriteRune(char)
		} else if char == ' ' {
			safeSession.WriteRune('_')
		}
	}
	safeStr := safeSession.String()
	if safeStr == "" {
		safeStr = "default"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	path := filepath.Join(home, fmt.Sprintf("invoke_chat_%s.json", safeStr))

	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		data, err := os.ReadFile(path)
		if err != nil {
			_ = json.NewEncoder(w).Encode(ChatState{History: []ChatHistoryEntry{}, Paths: []string{}})
			return
		}
		w.Write(data)
		return
	}

	if r.Method == http.MethodPost {
		var state ChatState
		if json.NewDecoder(r.Body).Decode(&state) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		out, _ := json.MarshalIndent(state, "", "  ")
		_ = os.WriteFile(path, out, 0644)
		w.WriteHeader(200)
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
