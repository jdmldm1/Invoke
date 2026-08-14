# Invoke

Air-gapped powershell terminal for windows, with file editor and tools.

![Invoke terminal](docs/screenshots/terminal.png)

---

## Subcommands & Features

### Editor & Diffs
* `pt edit <file>` : Open file in Monaco editor
* `pt diff <file>` : Side-by-side diff vs Git `HEAD`

![Monaco editor](docs/screenshots/editor.png)
![Diff vs HEAD](docs/screenshots/diff.png)

### Source Control
* `pt git` : Visual Git status and side-by-side branch review

![Git delta view](docs/screenshots/gitview.png)

### Port Utility
* `pt ports` : List listening ports and kill associated processes

![Port manager](docs/screenshots/ports.png)

---


### Key Bindings
* `Ctrl + \` : Toggle file sidebar
* `Ctrl + Shift + P` : Command search palette
* `Ctrl + Shift + K` : Workspace scratchpad pane
* `Ctrl + Shift + T / W` : Open / Close tab
* `Ctrl + Shift + D / S` : Split / Stack panes

### Build & Run
```powershell
# 1. Build server
go build -ldflags "-H windowsgui" -o invoke-server.exe .

# 2. Build app launcher
go build -ldflags "-H windowsgui" -o invoke-app.exe .\cmd\invoke-app\

# 3. Run
.\invoke-app.exe
```
