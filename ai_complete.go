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

func handleAIComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, 405)
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

	payload := map[string]interface{}{
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
		http.Error(w, `{"error":"POST required"}`, 405)
		return
	}

	var req struct {
		Query string `json:"query"`
		CWD   string `json:"cwd"`
		OS    string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Query) == "" {
		json.NewEncoder(w).Encode(map[string]string{"command": ""})
		return
	}

	cfg := loadConfig()

	osHint := "Windows PowerShell"
	if strings.ToLower(req.OS) == "linux" {
		osHint = "Linux bash"
	}

	systemPrompt := "You are a natural language to shell command translator for " + osHint + ". " +
		"Translate the user's description into a single, clean, executable command. " +
		"Reply with ONLY the raw command, nothing else. " +
		"Do NOT write any explanation, do NOT wrap it in markdown code blocks, do NOT write backticks. " +
		"If there is no sensible translation, reply with an empty string."

	userPrompt := "Current directory: " + req.CWD + "\nTranslate: " + req.Query

	payload := map[string]interface{}{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream":      false,
		"num_predict": 120,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 6 * time.Second}
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
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"command": ""})
		return
	}

	cmd := strings.TrimSpace(ollamaResp.Message.Content)
	cmd = strings.TrimPrefix(cmd, "```powershell")
	cmd = strings.TrimPrefix(cmd, "```bash")
	cmd = strings.TrimPrefix(cmd, "```")
	cmd = strings.TrimSuffix(cmd, "```")
	cmd = strings.Trim(cmd, "`'\"")
	cmd = strings.TrimSpace(cmd)

	if idx := strings.IndexByte(cmd, '\n'); idx >= 0 {
		cmd = cmd[:idx]
	}

	json.NewEncoder(w).Encode(map[string]string{"command": cmd})
}

func handleAIEditCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, 405)
		return
	}

	var req struct {
		Code        string `json:"code"`
		Instruction string `json:"instruction"`
		Lang        string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Instruction) == "" {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}

	cfg := loadConfig()

	systemPrompt := "You are an expert AI software engineer. " +
		"Your task is to modify the provided code block according to the instruction. " +
		"Maintain the exact coding style, indentation, and structure of the surrounding code where possible. " +
		"Reply with ONLY the raw modified code block. Do NOT write any explanations, do NOT wrap the code in backticks, and do NOT use markdown format."

	userPrompt := "Language: " + req.Lang + "\n" +
		"Instruction: " + req.Instruction + "\n\n" +
		"Code to modify:\n" + req.Code

	payload := map[string]interface{}{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream":      false,
		"temperature": 0.1,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 15 * time.Second}
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
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"code": ""})
		return
	}

	resCode := strings.TrimSpace(ollamaResp.Message.Content)
	resCode = strings.TrimPrefix(resCode, "```"+req.Lang)
	resCode = strings.TrimPrefix(resCode, "```")
	resCode = strings.TrimSuffix(resCode, "```")
	resCode = strings.Trim(resCode, "`")

	json.NewEncoder(w).Encode(map[string]string{"code": resCode})
}

func handleAIScaffoldScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, 405)
		return
	}

	var req struct {
		Desc string `json:"desc"`
		Lang string `json:"lang"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Desc) == "" {
		http.Error(w, "bad request", 400)
		return
	}

	ext := ".sh"
	switch strings.ToLower(req.Lang) {
	case "powershell":
		ext = ".ps1"
	case "python":
		ext = ".py"
	case "javascript", "node":
		ext = ".js"
	case "typescript":
		ext = ".ts"
	case "go":
		ext = ".go"
	case "csharp", "dotnet":
		ext = ".cs"
	case "svelte":
		ext = ".svelte"
	case "yaml", "helm", "kubernetes":
		ext = ".yaml"
	case "json":
		ext = ".json"
	case "xml":
		ext = ".xml"
	case "xsd":
		ext = ".xsd"
	case "html":
		ext = ".html"
	case "shell", "bash":
		ext = ".sh"
	}

	safeName := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return -1
	}, req.Name)
	if safeName == "" {
		safeName = "scaffold"
	}

	os.MkdirAll("scripts", 0755)
	targetFile := filepath.Join("scripts", safeName+ext)

	cfg := loadConfig()

	systemPrompt := "You are an expert software engineer. " +
		"Write a complete, fully functioning, clean, and well-documented script in " + req.Lang + " based on the user's description. " +
		"Ensure you include appropriate imports, error handling, and comments. " +
		"Reply with ONLY the raw script code. Do NOT wrap it in markdown code blocks, do NOT write backticks, and do NOT write explanations."

	payload := map[string]interface{}{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Desc},
		},
		"stream":      false,
		"temperature": 0.2,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "AI connection failed: "+err.Error(), 500)
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
		http.Error(w, "JSON parse error", 500)
		return
	}

	scriptCode := strings.TrimSpace(ollamaResp.Message.Content)
	scriptCode = strings.TrimPrefix(scriptCode, "```"+strings.ToLower(req.Lang))
	scriptCode = strings.TrimPrefix(scriptCode, "```")
	scriptCode = strings.TrimSuffix(scriptCode, "```")
	scriptCode = strings.Trim(scriptCode, "`")
	scriptCode = strings.TrimSpace(scriptCode) + "\n"

	absPath, _ := filepath.Abs(targetFile)
	if err := os.WriteFile(absPath, []byte(scriptCode), 0755); err != nil {
		http.Error(w, "File write error: "+err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "file": absPath})
}

type AIExplainHoverRequest struct {
	Symbol  string `json:"symbol"`
	Line    string `json:"line"`
	Context string `json:"context"`
	Lang    string `json:"lang"`
}

type AIExplainHoverResponse struct {
	Explanation string `json:"explanation"`
}

func handleAIExplainHover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, 405)
		return
	}

	var req AIExplainHoverRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Symbol) == "" {
		json.NewEncoder(w).Encode(AIExplainHoverResponse{Explanation: ""})
		return
	}

	cfg := loadConfig()

	systemPrompt := "You are an expert developer assistant. Explain the selected code symbol, programming keyword, or API function in a very concise, VS Code-style hover tooltip format (1-3 sentences maximum). Provide a short usage tip or example if helpful. Use clean markdown formatting (bold text, inline code, or brief code blocks). Keep it focused and brief."

	userPrompt := fmt.Sprintf("Language: %s\nHovered Symbol: '%s'\nLine content: '%s'\nSurrounding Context:\n%s", req.Lang, req.Symbol, req.Line, req.Context)

	payload := map[string]interface{}{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"stream":      false,
		"temperature": 0.2,
	}

	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(cfg.OllamaHost+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		json.NewEncoder(w).Encode(AIExplainHoverResponse{Explanation: "AI Explanation unavailable: " + err.Error()})
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
		json.NewEncoder(w).Encode(AIExplainHoverResponse{Explanation: ""})
		return
	}

	explanation := strings.TrimSpace(ollamaResp.Message.Content)
	json.NewEncoder(w).Encode(AIExplainHoverResponse{Explanation: explanation})
}

func handleAIEditorComplete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, 405)
		return
	}

	var req struct {
		Before string `json:"before"`
		Lang   string `json:"lang"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Before) == "" {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	cfg := loadConfig()

	systemPrompt := "You are an inline code completion assistant. " +
		"Complete the code immediately following the cursor in a " + req.Lang + " file. " +
		"Return ONLY the suffix characters or lines that should complete the current line or block. " +
		"Do NOT write markdown blocks, do NOT write backticks, do NOT write explanations. " +
		"If there is nothing sensible to add, return an empty string."

	payload := map[string]interface{}{
		"model": cfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": req.Before},
		},
		"stream":      false,
		"num_predict": 60,
		"temperature": 0.1,
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
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		json.NewEncoder(w).Encode(map[string]string{"completion": ""})
		return
	}

	completion := ollamaResp.Message.Content
	completion = strings.TrimPrefix(completion, "```" + req.Lang)
	completion = strings.TrimPrefix(completion, "```")
	completion = strings.TrimSuffix(completion, "```")
	completion = strings.Trim(completion, "`")

	json.NewEncoder(w).Encode(map[string]string{"completion": completion})
}

