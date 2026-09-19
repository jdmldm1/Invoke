# Invoke

Browser-based terminal + dev tools, for Windows and Linux.
 - **Desktop** (`invoke-app`): local window, local-only, no login.
 - **Server** (`invoke-server` / `InvokeService`): same UI over HTTP, reachable from other machines (by default). 

![Invoke terminal](docs/screenshots/terminal.png)

## Build & Run

```powershell
go build -ldflags "-H windowsgui" -o invoke-server.exe .
go build -ldflags "-H windowsgui" -o invoke-app.exe .\cmd\invoke-app\
.\invoke-app.exe
```

## Remote Access

`localhost` is always trusted, no password. Remote connections need a network access password:

* **System MSI**: prompted during install.
* **Interactive**: menu → *Remote Network Access...*
* **Headless**: `invoke-server.exe set-network-password <password>`

## Windows Service

```powershell
invoke-server.exe install-service
invoke-server.exe uninstall-service
```

## Subcommands

* `pt edit <file>` — Monaco editor
* `pt diff <file>` — diff vs Git `HEAD`
* `pt git` — visual git status / branch review
* `pt ports` — list/kill listening ports

## Key Bindings

* `Ctrl + \` — toggle file sidebar
* `Ctrl + Shift + P` — command palette
* `Ctrl + Shift + K` — scratchpad pane
* `Ctrl + Shift + T / W` — open / close tab
* `Ctrl + Shift + D / S` — split / stack panes
