# Invoke

A browser-based terminal workspace, an embedded editor, and developer utilities, served from a single binary. Air-gapped for offline use.

![Invoke terminal](docs/screenshots/terminal.png)

## Features

### Terminal
Terminal emulator (ConPTY ↔ WebSocket ↔ xterm.js) in a browser window, with tabbed and recursively split panes

### Terminal editor & diffs
Open and edit files in Microsoft's Monaco editor w/ syntax highlighting, line numbers, and a minimap.

![Monaco editor](docs/screenshots/editor.png)

`pt diff <file>` opens a side-by-side diff of the working copy against Git `HEAD`.

![Diff vs HEAD](docs/screenshots/diff.png)

### Visual Git delta
`pt git` subcommand opens a source-control view: a sidebar of changed files next to a live Monaco diff editor, so you can review your branch at a glance.

![Git delta view](docs/screenshots/gitview.png)

### Port manager
`pt ports` subcommand lists every bound port with its owning process, and lets you free a port.

![Port manager](docs/screenshots/ports.png)

---

## Build and Run

> **Requirements:** Go 1.21+, Windows (CGO not required)

Builds the desktop app — a slim launcher (`invoke-app.exe`) that starts the server (`invoke-server.exe`) in the background and wraps the UI in an embedded WebView2 window.

```powershell
# 1. Build the server (no console window)
go build -ldflags "-H windowsgui" -o invoke-server.exe .

# 2. Build the app launcher
$env:GOOS="windows"; $env:GOARCH="amd64"
go build -ldflags "-H windowsgui" -o invoke-app.exe .\cmd\invoke-app\

# 3. Keep both EXEs in the same directory, then launch:
.\invoke-app.exe
```

`invoke-app.exe` looks for `invoke-server.exe` in the same directory and starts it on a random free port. It requires the [WebView2 Runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/) (pre-installed on Windows 10/11). If WebView2 is not found, it falls back to opening your default browser.

A pre-built MSI installer (no administrator rights required) is available on the [Releases](../../releases) page.

---

## Shortcuts
* `Ctrl + \` : Toggle left file sidebar
* `Ctrl + Shift + P` : Open command search bar
* `Ctrl + Shift + K` : Open scratchpad pane
* `Ctrl + Shift + T / W` : New / close tab
* `Ctrl + Shift + D / S` : Split / stack panes

## Subcommands
Run these inside your Invoke terminal:
* `pt edit <file>` : Open file in the Monaco editor.
* `pt diff <file>` : Compare file against Git `HEAD`.
* `pt git` : Open the visual Git delta view.
* `pt ports` : List and free listening TCP ports.
* `pt ask <question>` : (if configured) Ask the local LLM (Ollama) a question.
* `pt explain "<error>"` : (if configured) Get a local LLM explanation and fix for an error.
* `pt gencommit` : Generate a commit message from the staged diff.
* `pt review` : AI review of the current diff.
