try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $OutputEncoding = [System.Text.Encoding]::UTF8
} catch {}

if ($env:INVOKE_EXE -and (Test-Path $env:INVOKE_EXE)) {
    $script:_ptBin = $env:INVOKE_EXE
} elseif ($PSScriptRoot) {
    $script:_ptBin = Join-Path $PSScriptRoot "invoke.exe"
} else {
    $script:_ptBin = "invoke.exe"
}
$script:_ptScript = $PSCommandPath
$script:_ptMarks  = Join-Path $env:USERPROFILE ".invoke_marks.json"

try {
    $script:_ptAdmin = ([Security.Principal.WindowsPrincipal]`
        [Security.Principal.WindowsIdentity]::GetCurrent()`
        ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
} catch { $script:_ptAdmin = $false }

if (-not ('Invoke.PtWindow' -as [type])) {
    try {
        Add-Type -ErrorAction Stop -TypeDefinition @'
using System;
using System.Runtime.InteropServices;

namespace Invoke {
    public static class PtWindow {
        const int GWL_EXSTYLE = -20;
        const int WS_EX_LAYERED = 0x80000;
        const uint LWA_ALPHA = 0x2;

        [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
        [DllImport("user32.dll")] static extern int GetWindowLong(IntPtr hWnd, int nIndex);
        [DllImport("user32.dll")] static extern int SetWindowLong(IntPtr hWnd, int nIndex, int dwNewLong);
        [DllImport("user32.dll")] static extern bool SetLayeredWindowAttributes(IntPtr hWnd, uint crKey, byte bAlpha, uint dwFlags);

        [StructLayout(LayoutKind.Sequential)]
        struct AccentPolicy { public int AccentState; public int AccentFlags; public uint GradientColor; public int AnimationId; }
        [StructLayout(LayoutKind.Sequential)]
        struct WinCompAttrData { public int Attribute; public IntPtr Data; public int SizeOfData; }
        [DllImport("user32.dll")] static extern int SetWindowCompositionAttribute(IntPtr hWnd, ref WinCompAttrData data);

        public static void SetOpacity(int percent) {
            if (percent < 30) percent = 30;
            if (percent > 100) percent = 100;
            IntPtr h = GetConsoleWindow();
            if (h == IntPtr.Zero) return;
            int ex = GetWindowLong(h, GWL_EXSTYLE);
            SetWindowLong(h, GWL_EXSTYLE, ex | WS_EX_LAYERED);
            byte alpha = (byte)(255 * percent / 100);
            SetLayeredWindowAttributes(h, 0, alpha, LWA_ALPHA);
        }

        public static void SetAcrylic(byte alpha, uint tintBGR) {
            IntPtr h = GetConsoleWindow();
            if (h == IntPtr.Zero) return;
            int ex = GetWindowLong(h, GWL_EXSTYLE);
            SetWindowLong(h, GWL_EXSTYLE, ex | WS_EX_LAYERED);
            AccentPolicy ac = new AccentPolicy();
            ac.AccentState = 4;
            ac.GradientColor = ((uint)alpha << 24) | (tintBGR & 0x00FFFFFF);
            int size = Marshal.SizeOf(ac);
            IntPtr ptr = Marshal.AllocHGlobal(size);
            Marshal.StructureToPtr(ac, ptr, false);
            WinCompAttrData data = new WinCompAttrData();
            data.Attribute = 19;
            data.Data = ptr;
            data.SizeOfData = size;
            SetWindowCompositionAttribute(h, ref data);
            Marshal.FreeHGlobal(ptr);
        }

        public static void SetSolid() {
            IntPtr h = GetConsoleWindow();
            if (h == IntPtr.Zero) return;
            AccentPolicy ac = new AccentPolicy();
            ac.AccentState = 0;
            int size = Marshal.SizeOf(ac);
            IntPtr ptr = Marshal.AllocHGlobal(size);
            Marshal.StructureToPtr(ac, ptr, false);
            WinCompAttrData data = new WinCompAttrData();
            data.Attribute = 19;
            data.Data = ptr;
            data.SizeOfData = size;
            SetWindowCompositionAttribute(h, ref data);
            Marshal.FreeHGlobal(ptr);
            SetLayeredWindowAttributes(h, 0, 255, LWA_ALPHA);
        }
    }
}
'@
    } catch {}
}

function Set-Opacity {
    param([int]$Percent = 90)
    try {
        [Invoke.PtWindow]::SetOpacity($Percent)
        Write-Host "Window opacity set to $Percent%." -ForegroundColor DarkGray
    } catch { Write-Host "Transparency is not available in this host." -ForegroundColor Yellow }
}
function Set-Acrylic {
    param([int]$Alpha = 125)
    if ($Alpha -lt 0)   { $Alpha = 0 }
    if ($Alpha -gt 255) { $Alpha = 255 }
    try { [Invoke.PtWindow]::SetAcrylic([byte]$Alpha, 0x0B0F14) } catch {}
}
function Set-Solid {
    try { [Invoke.PtWindow]::SetSolid(); Write-Host "Window set to solid (opaque)." -ForegroundColor DarkGray } catch {}
}

if (-not $env:WT_SESSION) {
    try { [Invoke.PtWindow]::SetAcrylic([byte]125, 0x0B0F14) } catch {}
}

function Open-PtView {
    param([string]$Kind, [string]$File)
    $abs = $File
    try { $abs = (Resolve-Path -LiteralPath $File -ErrorAction Stop).Path }
    catch { $abs = Join-Path (Get-Location).Path $File }

    if ($env:INVOKE_HOST) {
        try {
            $body = @{ type = $Kind; file = $abs } | ConvertTo-Json -Compress
            Invoke-RestMethod -Method Post -Uri "$($env:INVOKE_HOST)/open" -Body $body -ContentType 'application/json' -TimeoutSec 5 | Out-Null
            Write-Host "Opened $Kind for $(Split-Path $abs -Leaf) in a new tab." -ForegroundColor DarkGray
            return
        } catch {
            Write-Host "Could not reach the Invoke window; opening standalone." -ForegroundColor Yellow
        }
    }
    & $script:_ptBin $Kind $File
}

function pt {
    param (
        [Parameter(ValueFromRemainingArguments=$true)]
        [string[]]$Args
    )

    $binaryPath = $script:_ptBin
    $sub = if ($Args.Count -gt 0) { $Args[0] } else { "" }

    switch ($sub) {
        "help" { Show-PtHelp; return }

        "term" {
            Start-Process -FilePath $binaryPath -ArgumentList 'term' -WindowStyle Hidden
            Write-Host "Opening Invoke window..." -ForegroundColor DarkGray
            return
        }

        "explain" {
            if ($Error.Count -gt 0) {
                & $binaryPath explain $Error[0].Exception.Message
            } else {
                Write-Host "No recent PowerShell errors found to explain." -ForegroundColor Yellow
            }
            return
        }

        "ask" {
            if ($Args.Count -lt 2) {
                Write-Host "Usage: pt ask <your question>" -ForegroundColor Yellow
                return
            }
            & $binaryPath ask ($Args[1..($Args.Count-1)] -join " ")
            return
        }

        "commit" {
            $staged = git diff --staged --name-only 2>$null
            if (-not $staged) {
                Write-Host "Nothing staged. Stage changes first (try 'gaa' or 'git add <file>')." -ForegroundColor Yellow
                return
            }
            Write-Host "Generating commit message from staged diff..." -ForegroundColor DarkGray
            $msg = & $binaryPath gencommit
            if (-not $msg) {
                Write-Host "Could not generate a commit message (is the AI host reachable?)." -ForegroundColor Yellow
                return
            }
            Write-Host ""
            Write-Host "Suggested commit message:" -ForegroundColor Cyan
            Write-Host "  ------------------------------------------------------------" -ForegroundColor DarkGray
            $msg -split "`n" | ForEach-Object { Write-Host "  $_" }
            Write-Host "  ------------------------------------------------------------" -ForegroundColor DarkGray
            $ans = Read-Host "[Enter] commit / [e] edit / [n] cancel"

            $tmp = Join-Path $env:TEMP ("ptcommit_{0}.txt" -f ([guid]::NewGuid().ToString('N')))
            Set-Content -Path $tmp -Value $msg -Encoding UTF8
            switch -Regex ($ans) {
                '^[nN]' { Write-Host "Cancelled." -ForegroundColor Red; Remove-Item $tmp -Force -ErrorAction SilentlyContinue; return }
                '^[eE]' { Start-Process notepad -ArgumentList $tmp -Wait }
            }
            git commit -F $tmp
            Remove-Item $tmp -Force -ErrorAction SilentlyContinue
            return
        }

        "review" {
            & $binaryPath review
            return
        }

        "do" {
            if ($Args.Count -lt 2) {
                Write-Host "Usage: pt do <natural language request>" -ForegroundColor Yellow
                return
            }
            & $binaryPath do ($Args[1..($Args.Count-1)] -join " ")
            $actionFile = Join-Path $env:USERPROFILE ".invoke_action.json"
            if (Test-Path $actionFile) {
                $action = Get-Content $actionFile -Raw | ConvertFrom-Json
                Remove-Item $actionFile -Force
                if ($action.cmd -and $action.confirm -eq "true") {
                    $ans = Read-Host "`nExecute this command natively? [y/N]"
                    if ($ans -match "^y") {
                        Write-Host "Executing..." -ForegroundColor Green
                        Invoke-Expression $action.cmd
                    } else {
                        Write-Host "Cancelled." -ForegroundColor Red
                    }
                }
            }
            return
        }

        "edit" {
            if ($Args.Count -lt 2) {
                Write-Host "Usage: pt edit <file>" -ForegroundColor Yellow
                return
            }
            Open-PtView 'edit' ($Args[1..($Args.Count-1)] -join " ")
            return
        }

        "diff" {
            if ($Args.Count -lt 2) {
                Write-Host "Usage: pt diff <file>" -ForegroundColor Yellow
                return
            }
            Open-PtView 'diff' ($Args[1..($Args.Count-1)] -join " ")
            return
        }

        "git" {
            & $binaryPath git
            return
        }

        "ports" {
            Write-Host ""
            Write-Host "  Listening ports" -ForegroundColor Cyan
            $rows = Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
                Sort-Object LocalPort -Unique |
                Select-Object @{N='Port';E={$_.LocalPort}},
                              @{N='PID';E={$_.OwningProcess}},
                              @{N='Process';E={ (Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue).ProcessName }}
            if ($rows) { $rows | Format-Table -AutoSize } else { Write-Host "  None found." -ForegroundColor DarkGray }
            Write-Host "  Tip: 'killport <port>' frees one." -ForegroundColor DarkGray
            return
        }

        "env" {
            $filter = if ($Args.Count -ge 2) { $Args[1..($Args.Count-1)] -join " " } else { "" }
            Write-Host ""
            Get-ChildItem Env: | Sort-Object Name | Where-Object { -not $filter -or $_.Name -like "*$filter*" } | ForEach-Object {
                Write-Host ("  {0,-26}" -f $_.Name) -ForegroundColor Cyan -NoNewline
                if ($_.Name -in 'Path','PSModulePath','PATHEXT') {
                    Write-Host ""
                    ($_.Value -split ';') | Where-Object { $_ } | ForEach-Object { Write-Host "      $_" -ForegroundColor DarkGray }
                } else {
                    $v = $_.Value
                    if ($v.Length -gt 100) { $v = $v.Substring(0,100) + " ..." }
                    Write-Host " $v"
                }
            }
            return
        }

        default {
            Show-PtHelp
            return
        }
    }
}

function g       { git @args }
function gs      { git status -sb @args }
function ga      { git add @args }
function gaa     { git add -A @args }
function gco     { git checkout @args }
function gcb     { git checkout -b @args }
function gd      { git diff @args }
function gds     { git diff --staged @args }
function gst     { git stash @args }
function gsp     { git stash pop @args }
function gcommit { git commit -m @args }
function gamend  { git commit --amend @args }
function gpush   { git push @args }
function gpull   { git pull @args }
function gfetch  { git fetch --all --prune @args }
function gbranch { git branch @args }
function glog    { git log --oneline --graph --decorate -20 @args }
function glg     { git log --graph --pretty=format:'%C(yellow)%h%Creset %C(cyan)%an%Creset %s %C(green)(%cr)%Creset' -25 @args }

function ..   { Set-Location .. }
function ...  { Set-Location ../.. }
function .... { Set-Location ../../.. }
function ll   { Get-ChildItem @args }
function la   { Get-ChildItem -Force @args }
function which { param([string]$Name) (Get-Command $Name -ErrorAction SilentlyContinue).Source }
function mkcd { param([Parameter(Mandatory)][string]$Path) New-Item -ItemType Directory -Force -Path $Path | Out-Null; Set-Location $Path }
function touch {
    param([Parameter(Mandatory)][string]$Path)
    if (Test-Path $Path) { (Get-Item $Path).LastWriteTime = Get-Date }
    else { New-Item -ItemType File -Path $Path | Out-Null }
}
function reload { . $script:_ptScript; Write-Host "Invoke reloaded." -ForegroundColor Green }

function killport {
    param([Parameter(Mandatory)][int]$Port)
    $conns = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if (-not $conns) { Write-Host "Nothing is listening on port $Port." -ForegroundColor Yellow; return }
    $conns | Select-Object -ExpandProperty OwningProcess -Unique | ForEach-Object {
        $proc = Get-Process -Id $_ -ErrorAction SilentlyContinue
        if ($proc) {
            Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue
            Write-Host "Killed $($proc.ProcessName) (PID $_) on port $Port." -ForegroundColor Green
        }
    }
}

function serve {
    param([int]$Port = 8000)
    if (Get-Command python -ErrorAction SilentlyContinue) {
        Write-Host "Serving $($PWD.Path) at http://localhost:$Port  (Ctrl+C to stop)" -ForegroundColor Cyan
        python -m http.server $Port
    } elseif (Get-Command npx -ErrorAction SilentlyContinue) {
        Write-Host "Serving via npx serve at port $Port (Ctrl+C to stop)" -ForegroundColor Cyan
        npx --yes serve -l $Port
    } else {
        Write-Host "Need Python or Node (npx) to serve. Neither was found." -ForegroundColor Yellow
    }
}

function json {
    param([Parameter(ValueFromPipeline=$true, ValueFromRemainingArguments=$true)][string[]]$InputJson)
    process {
        $text = if ($InputJson) { $InputJson -join "`n" } else { Get-Clipboard -Raw }
        if (-not $text) { Write-Host "No JSON provided (pipe it in or copy to clipboard)." -ForegroundColor Yellow; return }
        try { $text | ConvertFrom-Json | ConvertTo-Json -Depth 20 }
        catch { Write-Host "Invalid JSON: $($_.Exception.Message)" -ForegroundColor Red }
    }
}

function mark {
    param([string]$Name)
    if (-not $Name) { $Name = Split-Path $PWD -Leaf }
    $marks = @{}
    if (Test-Path $script:_ptMarks) {
        (Get-Content $script:_ptMarks -Raw | ConvertFrom-Json).PSObject.Properties |
            ForEach-Object { $marks[$_.Name] = $_.Value }
    }
    $marks[$Name] = $PWD.Path
    $marks | ConvertTo-Json | Set-Content $script:_ptMarks -Encoding UTF8
    Write-Host "Bookmarked '$Name' -> $($PWD.Path)" -ForegroundColor Green
}
function j {
    param([string]$Name)
    if (-not (Test-Path $script:_ptMarks)) {
        Write-Host "No bookmarks yet. Use 'mark' to save the current directory." -ForegroundColor Yellow; return
    }
    $target = (Get-Content $script:_ptMarks -Raw | ConvertFrom-Json).$Name
    if ($target -and (Test-Path $target)) { Set-Location $target }
    else { Write-Host "No bookmark named '$Name'." -ForegroundColor Yellow }
}
function marks {
    if (-not (Test-Path $script:_ptMarks)) { Write-Host "No bookmarks." -ForegroundColor Yellow; return }
    (Get-Content $script:_ptMarks -Raw | ConvertFrom-Json).PSObject.Properties | ForEach-Object {
        Write-Host ("  {0,-16}" -f $_.Name) -ForegroundColor Cyan -NoNewline
        Write-Host $_.Value -ForegroundColor DarkGray
    }
}
Register-ArgumentCompleter -CommandName j -ParameterName Name -ScriptBlock {
    param($cmd, $param, $word)
    $f = Join-Path $env:USERPROFILE ".invoke_marks.json"
    if (Test-Path $f) {
        (Get-Content $f -Raw | ConvertFrom-Json).PSObject.Properties.Name |
            Where-Object { $_ -like "$word*" } |
            ForEach-Object { [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_) }
    }
}

$script:_ptZFile      = Join-Path $env:USERPROFILE ".invoke_z.json"
$global:_ptZ          = @{}
$script:_ptZLastWrite = 0
if (Test-Path $script:_ptZFile) {
    try {
        $obj = Get-Content $script:_ptZFile -Raw | ConvertFrom-Json
        foreach ($prop in $obj.PSObject.Properties) {
            $global:_ptZ[$prop.Name] = @{ n = [double]$prop.Value.n; t = [long]$prop.Value.t }
        }
    } catch {}
}
function _ptTrackDir {
    param([string]$path)
    if (-not $path) { return }
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    if ($global:_ptZ.ContainsKey($path)) {
        $global:_ptZ[$path].n = [double]$global:_ptZ[$path].n + 1
        $global:_ptZ[$path].t = $now
    } else {
        $global:_ptZ[$path] = @{ n = 1.0; t = $now }
    }
    if (($now - $script:_ptZLastWrite) -ge 5) {
        $script:_ptZLastWrite = $now
        try { $global:_ptZ | ConvertTo-Json -Depth 5 | Set-Content $script:_ptZFile -Encoding UTF8 } catch {}
    }
}
function z {
    param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Query)
    if (-not $Query) {
        $global:_ptZ.GetEnumerator() | Sort-Object { $_.Value.n } -Descending |
            Select-Object -First 15 | ForEach-Object {
                Write-Host ("  {0,6:N0}  " -f $_.Value.n) -ForegroundColor DarkGray -NoNewline
                Write-Host $_.Key
            }
        return
    }
    $q = ($Query -join ' ')
    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    $best = $null; $bestScore = -1
    foreach ($kv in $global:_ptZ.GetEnumerator()) {
        $path = $kv.Key
        if ($path -notmatch [regex]::Escape($q)) { continue }
        if (-not (Test-Path $path)) { continue }
        $age = $now - [long]$kv.Value.t
        $rec = if ($age -lt 3600) { 4 } elseif ($age -lt 86400) { 2 } elseif ($age -lt 604800) { 1 } else { 0.5 }
        $score = [double]$kv.Value.n * $rec
        if ($score -gt $bestScore) { $bestScore = $score; $best = $path }
    }
    if ($best) { Set-Location $best }
    else { Write-Host "z: no matching directory for '$q'." -ForegroundColor Yellow }
}
Register-ArgumentCompleter -CommandName z -ScriptBlock {
    param($a, $b, $word)
    $f = Join-Path $env:USERPROFILE ".invoke_z.json"
    if (Test-Path $f) {
        try {
            (Get-Content $f -Raw | ConvertFrom-Json).PSObject.Properties.Name |
                Where-Object { (Split-Path $_ -Leaf) -like "*$word*" } |
                Select-Object -Unique -First 10 |
                ForEach-Object {
                    $leaf = Split-Path $_ -Leaf
                    [System.Management.Automation.CompletionResult]::new($leaf, "$leaf  ($_)", 'ParameterValue', $_)
                }
        } catch {}
    }
}

if (Get-Command Set-PSReadLineOption -ErrorAction SilentlyContinue) {
    try { Set-PSReadLineOption -EditMode Windows } catch {}
    try { Set-PSReadLineOption -HistorySearchCursorMovesToEnd } catch {}
    try { Set-PSReadLineOption -BellStyle None } catch {}
    try { Set-PSReadLineOption -PredictionSource HistoryAndPlugin } catch {
        try { Set-PSReadLineOption -PredictionSource History } catch {}
    }
    try { Set-PSReadLineOption -PredictionViewStyle ListView } catch {}
    try {
        Set-PSReadLineOption -Colors @{
            Command   = '#81a1c1'
            Parameter = '#9aa3b5'
            Operator  = '#d8dee9'
            Variable  = '#8fbcbb'
            String    = '#a3be8c'
            Number    = '#b48ead'
            Comment   = '#616e88'
            Keyword   = '#5e81ac'
            Error     = '#bf616a'
            Selection = '#3b4252'
            InlinePrediction = '#4c566a'
        }
    } catch {}
    try { Set-PSReadLineKeyHandler -Key Tab            -Function MenuComplete } catch {}
    try { Set-PSReadLineKeyHandler -Key UpArrow        -Function HistorySearchBackward } catch {}
    try { Set-PSReadLineKeyHandler -Key DownArrow      -Function HistorySearchForward } catch {}
    try { Set-PSReadLineKeyHandler -Key Ctrl+w         -Function BackwardKillWord } catch {}
    try { Set-PSReadLineKeyHandler -Key Ctrl+RightArrow -Function ForwardWord } catch {}
}

if (Get-Command Set-PSReadLineKeyHandler -ErrorAction SilentlyContinue) {
    Set-PSReadLineKeyHandler -Key "Ctrl+g" -ScriptBlock {
        $line = $null; $cursor = $null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)
        if ([string]::IsNullOrWhiteSpace($line)) { return }
        [Microsoft.PowerShell.PSConsoleReadLine]::AddToHistory($line)
        $gen = & $script:_ptBin gen $line 2>$null
        if ($gen) {
            [Microsoft.PowerShell.PSConsoleReadLine]::RevertLine()
            [Microsoft.PowerShell.PSConsoleReadLine]::Insert(($gen -join " ").Trim())
        }
    }
}

if (Get-Command Set-PSReadLineKeyHandler -ErrorAction SilentlyContinue) {
    Set-PSReadLineKeyHandler -Key "Ctrl+h" -ScriptBlock {
        $ast=$null;$tokens=$null;$errors=$null;$cursor=$null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$ast,[ref]$tokens,[ref]$errors,[ref]$cursor)
        $cur = $null
        foreach ($t in $tokens) { if ($cursor -ge $t.Extent.StartOffset -and $cursor -le $t.Extent.EndOffset) { $cur = $t; break } }
        if ($cur -and $cur.Text) {
            $word = $cur.Text; $bin = $script:_ptBin
            if ($env:WT_SESSION -and (Get-Command wt -ErrorAction SilentlyContinue)) {
                $cmd = "& '$bin' info '$word'; Write-Host '`nPress any key to close...'; `$null = `$Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown'); exit"
                $encoded = [Convert]::ToBase64String([System.Text.Encoding]::Unicode.GetBytes($cmd))
                Start-Process wt -ArgumentList "-w 0 split-pane -H -s 0.3 powershell.exe -NoProfile -EncodedCommand $encoded" -NoNewWindow
            } else {
                [Console]::WriteLine(""); & $bin info $word
                [Microsoft.PowerShell.PSConsoleReadLine]::InvokePrompt()
            }
        } else {
            [Console]::WriteLine(""); Write-Host "No command under cursor." -ForegroundColor Yellow
            [Microsoft.PowerShell.PSConsoleReadLine]::InvokePrompt()
        }
    }
}

function Show-PtHelp {
    $c = 'Cyan'; $d = 'DarkGray'
    Write-Host ""
    Write-Host "  Invoke - command reference" -ForegroundColor Magenta
    Write-Host "  ----------------------------------------------------------------" -ForegroundColor $d
    Write-Host "  Window" -ForegroundColor White
    Write-Host "    pt term       " -ForegroundColor $c -NoNewline; Write-Host "Multi-pane terminal window: tabs + split panes"
    Write-Host "  AI" -ForegroundColor White
    Write-Host "    pt help       " -ForegroundColor $c -NoNewline; Write-Host "Show this reference (also: bare 'pt')"
    Write-Host "    pt do <text>  " -ForegroundColor $c -NoNewline; Write-Host "Generate + optionally run a command"
    Write-Host "    pt ask <text> " -ForegroundColor $c -NoNewline; Write-Host "Ask the AI a general question"
    Write-Host "    pt commit     " -ForegroundColor $c -NoNewline; Write-Host "AI commit message from staged diff (edit/confirm)"
    Write-Host "    pt review     " -ForegroundColor $c -NoNewline; Write-Host "AI code review of your uncommitted changes"
    Write-Host "    pt explain    " -ForegroundColor $c -NoNewline; Write-Host "Explain the last error"
    Write-Host "    Ctrl+G        " -ForegroundColor $c -NoNewline; Write-Host "Convert the typed line into a command inline"
    Write-Host "    Ctrl+H        " -ForegroundColor $c -NoNewline; Write-Host "AI hover info for the word under the cursor"
    Write-Host "  Code + Git (browser views)" -ForegroundColor White
    Write-Host "    pt git        " -ForegroundColor $c -NoNewline; Write-Host "Browse all working-tree deltas side-by-side"
    Write-Host "    pt diff <f>   " -ForegroundColor $c -NoNewline; Write-Host "Diff vs HEAD (opens a tab in the window)"
    Write-Host "    pt edit <f>   " -ForegroundColor $c -NoNewline; Write-Host "Edit in Monaco (opens a tab in the window)"
    Write-Host "  Git shortcuts" -ForegroundColor White
    Write-Host "    gs ga gaa     " -ForegroundColor $c -NoNewline; Write-Host "status / add / add -A"
    Write-Host "    gcommit `"m`"   " -ForegroundColor $c -NoNewline; Write-Host "commit -m   (gamend, gpush, gpull, gfetch)"
    Write-Host "    gco gcb gbranch" -ForegroundColor $c -NoNewline; Write-Host " checkout / checkout -b / branch"
    Write-Host "    gd gds glog glg" -ForegroundColor $c -NoNewline; Write-Host " diff / diff --staged / log / graph log"
    Write-Host "  System" -ForegroundColor White
    Write-Host "    pt ports      " -ForegroundColor $c -NoNewline; Write-Host "List listening ports + owning process"
    Write-Host "    pt env [find] " -ForegroundColor $c -NoNewline; Write-Host "List/search environment variables"
    Write-Host "    killport <p>  " -ForegroundColor $c -NoNewline; Write-Host "Kill the process listening on a port"
    Write-Host "    serve [port]  " -ForegroundColor $c -NoNewline; Write-Host "Serve the current dir over HTTP"
    Write-Host "    json          " -ForegroundColor $c -NoNewline; Write-Host "Pretty-print JSON (pipe in or from clipboard)"
    Write-Host "  Navigation" -ForegroundColor White
    Write-Host "    .. ... ....   " -ForegroundColor $c -NoNewline; Write-Host "Up 1 / 2 / 3 directories"
    Write-Host "    mkcd touch which ll la" -ForegroundColor $c -NoNewline; Write-Host "  Unix-style helpers"
    Write-Host "    mark / j / marks" -ForegroundColor $c -NoNewline; Write-Host " Bookmark dir / jump (Tab-completes) / list"
    Write-Host "    z <partial>   " -ForegroundColor $c -NoNewline; Write-Host "Jump to a frecent directory (auto-tracked)"
    Write-Host "    reload        " -ForegroundColor $c -NoNewline; Write-Host "Reload this profile"
    Write-Host "  Appearance" -ForegroundColor White
    Write-Host "    Set-Acrylic <0-255>" -ForegroundColor $c -NoNewline; Write-Host " Frosted-glass blur (lower = more see-through)"
    Write-Host "    Set-Opacity <30-100>" -ForegroundColor $c -NoNewline; Write-Host " Plain window transparency percent"
    Write-Host "    Set-Solid     " -ForegroundColor $c -NoNewline; Write-Host "Turn transparency off"
    Write-Host ""
}

Set-Alias -Name iv -Value pt -Scope Global -Force
Set-Alias -Name invoke -Value pt -Scope Global -Force

function prompt {
    $ok       = $?
    $lastExit = $global:LASTEXITCODE
    $esc      = [char]27

    $r="$esc[0m"
    $dim="$esc[38;2;97;110;136m"
    $blue="$esc[38;2;129;161;193m"
    $cyan="$esc[38;2;216;222;233m"
    $green="$esc[38;2;163;190;140m"
    $red="$esc[38;2;191;97;106m"
    $yellow="$esc[38;2;235;203;139m"
    $mag="$esc[38;2;180;142;173m"
    $sky="$esc[38;2;129;161;193m"

    $bar=[char]0x258C; $sep=[char]0x2502; $arr=[char]0x276F; $x=[char]0x2718; $dot=[char]0x00B7

    try { _ptTrackDir $PWD.Path } catch {}

    $p = $PWD.Path
    if ($HOME -and $p.StartsWith($HOME)) { $p = "~" + $p.Substring($HOME.Length) }
    $line = "$sky$bar$r $cyan$p$r"

    $branchName = ''; $isDirty = $false
    $staged = 0; $unstaged = 0; $untracked = 0
    if (Get-Command git -ErrorAction SilentlyContinue) {
        $b = git branch --show-current 2>$null
        if ($b) {
            $branchName = $b
            $status = git status --porcelain 2>$null
            $isDirty = [bool]$status
            if ($isDirty) {
                foreach ($sLine in $status) {
                    if ($sLine.Length -ge 2) {
                        $x = $sLine[0]; $y = $sLine[1]
                        if ($x -eq '?') {
                            $untracked++
                        } else {
                            if ($x -ne ' ' -and $x -ne '?') { $staged++ }
                            if ($y -ne ' ' -and $y -ne '?') { $unstaged++ }
                        }
                    }
                }
                $line += "  $dim$sep$r $yellow$branchName*$r"
            } else {
                $line += "  $dim$sep$r $green$branchName$r"
            }
        }
    }

    $segs = @()
    if ($env:VIRTUAL_ENV) { $segs += "$mag($(Split-Path $env:VIRTUAL_ENV -Leaf))$r" }
    if ($script:_ptAdmin) { $segs += "${red}ADMIN$r" }
    $h = Get-History -Count 1 -ErrorAction SilentlyContinue
    if ($h) {
        $ts = $null
        if ($h.Duration) { $ts = $h.Duration }
        elseif ($h.EndExecutionTime -and $h.StartExecutionTime) { $ts = $h.EndExecutionTime - $h.StartExecutionTime }
        if ($ts) {
            $d = if ($ts.TotalSeconds -ge 1) { "{0:N2}s" -f $ts.TotalSeconds } elseif ($ts.TotalMilliseconds -ge 5) { "{0:N0}ms" -f $ts.TotalMilliseconds } else { "" }
            if ($d) { $segs += "$dim$d$r" }
        }
    }
    $segs += "$blue$(Get-Date -Format 'HH:mm:ss')$r"
    if (-not $ok) {
        $code = if ($lastExit -and $lastExit -ne 0) { " $lastExit" } else { "" }
        $segs += "$red$x$code$r"
    }
    if ($segs.Count -gt 0) { $line += "  $dim$dot$r  " + ($segs -join "  ") }

    $symColor = if ($ok) { $green } else { $red }

    $dirtyFlag = if ($isDirty) { '1' } else { '0' }
    $exitCode = if (-not $ok) {
        if ($null -ne $lastExit -and $lastExit -ne 0) { [int]$lastExit } else { 1 }
    } elseif ($null -ne $lastExit -and $lastExit -ne 0) { [int]$lastExit } else { 0 }
    $osc = "$esc]7000;$($PWD.Path)|$branchName|$dirtyFlag|$staged|$unstaged|$untracked|$exitCode$([char]7)"
    return "$osc`n$line`n$symColor$arr$r "
}
