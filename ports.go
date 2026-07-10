package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
)

type PortInfo struct {
	Port        int    `json:"Port"`
	PID         int    `json:"PID"`
	ProcessName string `json:"ProcessName"`
}

// handlePorts returns a JSON list of listening ports and their associated PIDs/processes.
func handlePorts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	cmdText := `Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue | ForEach-Object { [PSCustomObject]@{ Port = $_.LocalPort; PID = $_.OwningProcess; ProcessName = (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).Name } } | ConvertTo-Json`
	
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", cmdText)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		http.Error(w, fmt.Sprintf("Error fetching ports: %v, %s", err, stderr.String()), 500)
		return
	}

	output := stdout.Bytes()
	if len(bytes.TrimSpace(output)) == 0 {
		w.Write([]byte("[]"))
		return
	}

	w.Write(output)
}

// handlePortsKill terminates a process by PID.
// POST /ports/kill?pid=123
func handlePortsKill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	pidStr := r.URL.Query().Get("pid")
	if pidStr == "" {
		http.Error(w, `{"error":"pid parameter required"}`, 400)
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		http.Error(w, `{"error":"invalid pid"}`, 400)
		return
	}

	cmdText := fmt.Sprintf("Stop-Process -Id %d -Force", pid)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", cmdText)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		w.WriteHeader(500)
		json.NewEncoder(w).Encode(map[string]string{"error": stderr.String()})
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handlePortsHTML serves the Port Manager UI.
func handlePortsHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(portsHTMLTemplate))
}

const portsHTMLTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Port Manager</title>
    <style>
        :root {
            --bg: #0b0f14;
            --panel: #10151b;
            --panel2: #161d26;
            --border: #1c2530;
            --border2: #283545;
            --text: #d8dee9;
            --muted: #6f8092;
            --accent: #88c0d0;
            --bad: #bf616a;
            --good: #a3be8c;
        }
        body {
            margin: 0;
            background: var(--bg);
            color: var(--text);
            font-family: 'Segoe UI', system-ui, sans-serif;
            padding: 24px;
        }
        h2 {
            margin-top: 0;
            font-size: 20px;
            font-weight: 600;
            display: flex;
            align-items: center;
            gap: 10px;
        }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 20px;
        }
        .btn {
            background: var(--panel2);
            border: 1px solid var(--border2);
            color: var(--text);
            padding: 8px 16px;
            border-radius: 6px;
            cursor: pointer;
            font-size: 13px;
            font-weight: 600;
            display: flex;
            align-items: center;
            gap: 6px;
            transition: all 0.2s;
        }
        .btn:hover {
            background: var(--accent);
            color: #000;
            border-color: var(--accent);
        }
        .btn-kill {
            background: rgba(191, 97, 106, 0.15);
            border: 1px solid var(--bad);
            color: var(--bad);
            padding: 4px 10px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: bold;
        }
        .btn-kill:hover {
            background: var(--bad);
            color: var(--text);
        }
        table {
            width: 100%;
            border-collapse: collapse;
            background: var(--panel);
            border: 1px solid var(--border);
            border-radius: 8px;
            overflow: hidden;
        }
        th, td {
            padding: 12px 16px;
            text-align: left;
            border-bottom: 1px solid var(--border);
            font-size: 14px;
        }
        th {
            background: var(--panel2);
            font-weight: 600;
            color: var(--accent);
        }
        tr:last-child td {
            border-bottom: none;
        }
        tr:hover td {
            background: rgba(255, 255, 255, 0.02);
        }
        .status-tag {
            background: rgba(163, 190, 140, 0.15);
            color: var(--good);
            padding: 2px 6px;
            border-radius: 4px;
            font-size: 11px;
            font-weight: bold;
        }
        .empty {
            padding: 40px;
            text-align: center;
            color: var(--muted);
            font-size: 14px;
        }
    </style>
</head>
<body>
    <div class="header">
        <h2>🔌 Port Manager</h2>
        <button class="btn" onclick="loadPorts()">🔄 Refresh</button>
    </div>
    
    <table id="ports-table">
        <thead>
            <tr>
                <th>Port</th>
                <th>PID</th>
                <th>Process Name</th>
                <th>Status</th>
                <th>Action</th>
            </tr>
        </thead>
        <tbody id="ports-body">
            <tr>
                <td colspan="5" class="empty">Loading active ports...</td>
            </tr>
        </tbody>
    </table>

    <script>
        function loadPorts() {
            const tbody = document.getElementById('ports-body');
            fetch('/ports')
                .then(r => r.json())
                .then(data => {
                    tbody.innerHTML = '';
                    
                    // Handle single object vs array from PowerShell ConverTo-Json
                    let ports = [];
                    if (data) {
                        if (Array.isArray(data)) {
                            ports = data;
                        } else if (typeof data === 'object') {
                            ports = [data];
                        }
                    }

                    if (ports.length === 0) {
                        tbody.innerHTML = '<tr><td colspan="5" class="empty">No active listening ports found.</td></tr>';
                        return;
                    }

                    // Sort by port number
                    ports.sort((a, b) => a.Port - b.Port);

                    ports.forEach(p => {
                        const tr = document.createElement('tr');
                        
                        const tdPort = document.createElement('td');
                        tdPort.style.fontWeight = 'bold';
                        tdPort.textContent = p.Port;
                        
                        const tdPid = document.createElement('td');
                        tdPid.textContent = p.PID;
                        
                        const tdName = document.createElement('td');
                        tdName.textContent = p.ProcessName || '(unknown)';
                        
                        const tdStatus = document.createElement('td');
                        tdStatus.innerHTML = '<span class="status-tag">LISTENING</span>';
                        
                        const tdAction = document.createElement('td');
                        const killBtn = document.createElement('button');
                        killBtn.className = 'btn-kill';
                        killBtn.textContent = 'Kill Process';
                        killBtn.onclick = () => killProcess(p.PID, p.Port, p.ProcessName);
                        tdAction.appendChild(killBtn);
                        
                        tr.appendChild(tdPort);
                        tr.appendChild(tdPid);
                        tr.appendChild(tdName);
                        tr.appendChild(tdStatus);
                        tr.appendChild(tdAction);
                        
                        tbody.appendChild(tr);
                    });
                })
                .catch(err => {
                    tbody.innerHTML = '<tr><td colspan="5" class="empty" style="color:var(--bad)">Error loading ports: ' + err + '</td></tr>';
                });
        }

        function killProcess(pid, port, name) {
            if (confirm('Are you sure you want to stop process ' + (name || '') + ' (PID: ' + pid + ') holding port ' + port + '?')) {
                fetch('/ports/kill?pid=' + pid, { method: 'POST' })
                    .then(r => r.json())
                    .then(d => {
                        if (d.ok) {
                            loadPorts();
                        } else {
                            alert('Failed to stop process: ' + (d.error || 'unknown error'));
                        }
                    });
            }
        }

        window.onload = loadPorts;
    </script>
</body>
</html>
`
