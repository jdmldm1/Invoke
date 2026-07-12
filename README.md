# PowerTerm

An enhanced terminal emulator, embedded code editor, and git overview served from a single, self-contained binary. No internet required.

## Key Bindings
- `Ctrl + \` : Toggle file sidebar
- `Ctrl + Shift + P` : Command palette
- `Ctrl + Shift + K` : Scratchpad
- `Ctrl + Shift + T / W` : Tab open / close
- `Ctrl + Shift + D / S` : Split / Stack panes

## Inside PowerTerm
- `pt edit <file>` : Open Monaco editor
- `pt diff <file>` : Show git diff against HEAD
- `pt git` : Graphical Git status & diff
- `pt ports` : View and release listening ports
- `pt ask <q>` / `pt explain <err>` : Native Ollama helper

## Build & Run
```powershell
go build -ldflags "-H windowsgui" -o powerterm.exe .
.\powerterm.exe
```
