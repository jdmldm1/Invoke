package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func handleConverterRoute(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(converterPageHTML))
}

func handleConverterRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}

	var req struct {
		Code string `json:"code"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code == "" {
		http.Error(w, "bad request", 400)
		return
	}

	prompt := fmt.Sprintf(
		"You are a specialized scripting language translator. Translate this script/command from %s to %s.\n"+
			"Provide ONLY the translated code, without any markdown backticks, without any prefix/suffix explanation, and without additional comments unless required for functional correctness. "+
			"Here is the code to translate:\n\n%s",
		req.From, req.To, req.Code,
	)

	out, err := aiGenerateOnce(prompt, 60*time.Second)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "code": out})
}

const converterPageHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>AI Command Translator</title>
  <style>
    :root {
      --bg: #0b0f14; --panel: #10151b; --panel2: #161d26; --border: #1c2530; --border2: #283545;
      --text: #d8dee9; --muted: #6f8092; --accent: #88c0d0; --good: #a3be8c; --bad: #bf616a; --ink: #05070a;
    }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--bg); color: var(--text); font-family: 'Segoe UI', sans-serif; padding: 24px; }
    #container { max-width: 900px; margin: 0 auto; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 24px; box-shadow: 0 10px 30px rgba(0,0,0,0.5); }
    h2 { margin-top: 0; font-size: 20px; color: var(--accent); display: flex; align-items: center; gap: 8px; }
    .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 20px; }
    .col { display: flex; flex-direction: column; gap: 8px; }
    label { font-size: 13px; color: var(--muted); font-weight: 600; text-transform: uppercase; }
    textarea { width: 100%; height: 180px; background: var(--bg); color: var(--text); border: 1px solid var(--border2); border-radius: 8px; padding: 12px; font-family: 'Consolas', monospace; font-size: 13px; resize: vertical; outline: none; }
    textarea:focus { border-color: var(--accent); }
    select { width: 100%; background: var(--bg); color: var(--text); border: 1px solid var(--border2); border-radius: 6px; padding: 8px 12px; font-size: 13px; outline: none; }
    select:focus { border-color: var(--accent); }
    .actions { display: flex; align-items: center; gap: 12px; margin-bottom: 20px; }
    button { padding: 10px 20px; border: 0; border-radius: 6px; font-weight: 600; cursor: pointer; transition: opacity 0.2s; font-size: 13px; }
    .btn-primary { background: var(--accent); color: var(--ink); }
    .btn-secondary { background: var(--panel2); color: var(--text); border: 1px solid var(--border2); }
    button:hover { opacity: 0.9; }
    button:disabled { opacity: 0.5; cursor: default; }
    #status { font-size: 13px; color: var(--muted); }
  </style>
</head>
<body>
  <div id="container">
    <h2>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="color:var(--accent)">
        <polyline points="16 3 21 8 16 13"></polyline>
        <line x1="21" y1="8" x2="9" y2="8"></line>
        <polyline points="8 21 3 16 8 11"></polyline>
        <line x1="3" y1="16" x2="15" y2="16"></line>
      </svg>
      AI Script & Command Translator
    </h2>
    <p style="font-size:13px; color:var(--muted); margin-bottom:20px; margin-top:0;">Translate commands and shell scripts between Linux (Bash/Sh) and Windows (PowerShell/CMD) instantly.</p>
    
    <div class="grid">
      <div class="col">
        <label for="from-lang">From</label>
        <select id="from-lang">
          <option value="Linux Bash">Linux Bash</option>
          <option value="Windows PowerShell">Windows PowerShell</option>
          <option value="Windows CMD Command Prompt">Windows CMD</option>
        </select>
      </div>
      <div class="col">
        <label for="to-lang">To</label>
        <select id="to-lang">
          <option value="Windows PowerShell">Windows PowerShell</option>
          <option value="Linux Bash" selected>Linux Bash</option>
          <option value="Windows CMD Command Prompt">Windows CMD</option>
        </select>
      </div>
    </div>

    <div class="col" style="margin-bottom: 20px;">
      <label for="source">Source Command / Script</label>
      <textarea id="source" placeholder="Paste your script or command here... (e.g. find . -name '*.log' -mtime +7 -delete)"></textarea>
    </div>

    <div class="actions">
      <button id="convert-btn" class="btn-primary">Translate Script</button>
      <span id="status"></span>
    </div>

    <div class="col" style="position:relative;">
      <label for="result">Translated Result</label>
      <textarea id="result" readonly placeholder="Translated script will appear here..."></textarea>
      <button id="copy-btn" class="btn-secondary" style="position:absolute; top:28px; right:10px; padding:6px 12px; font-size:11px;">Copy</button>
    </div>
  </div>

  <script>
    const convertBtn = document.getElementById('convert-btn');
    const copyBtn = document.getElementById('copy-btn');
    const source = document.getElementById('source');
    const result = document.getElementById('result');
    const fromLang = document.getElementById('from-lang');
    const toLang = document.getElementById('to-lang');
    const status = document.getElementById('status');

    convertBtn.onclick = function() {
      const code = source.value.trim();
      if (!code) return;

      convertBtn.disabled = true;
      status.textContent = 'Translating using Ollama...';
      result.value = '';

      fetch('/converter/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ code: code, from: fromLang.value, to: toLang.value })
      })
      .then(r => r.json())
      .then(res => {
        if (res.success) {
          result.value = res.code;
          status.textContent = 'Translation successful.';
        } else {
          result.value = 'Error: ' + res.error;
          status.textContent = 'Translation failed.';
        }
      })
      .catch(err => {
        result.value = 'Connection error: ' + err.message;
        status.textContent = 'Failed to connect.';
      })
      .finally(() => {
        convertBtn.disabled = false;
      });
    };

    copyBtn.onclick = function() {
      if (!result.value) return;
      navigator.clipboard.writeText(result.value);
      const oldTxt = copyBtn.textContent;
      copyBtn.textContent = 'Copied!';
      setTimeout(() => copyBtn.textContent = oldTxt, 1500);
    };
  </script>
</body>
</html>`
