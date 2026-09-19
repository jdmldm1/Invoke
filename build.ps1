param(
    [switch]$SkipMSI,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

$ProjectRoot = $PSScriptRoot
$BuildDir = Join-Path $ProjectRoot "build"
$InstallerDir = Join-Path $ProjectRoot "installer"

Write-Host "Invoke Build Script" -ForegroundColor Cyan
Write-Host "======================" -ForegroundColor Cyan
Write-Host ""

if ($Clean) {
    Write-Host "Cleaning build directory..." -ForegroundColor Yellow
    if (Test-Path $BuildDir) {
        Remove-Item -Path $BuildDir -Recurse -Force
    }
    New-Item -Path $BuildDir -ItemType Directory -Force | Out-Null
    Write-Host "Build directory cleaned." -ForegroundColor Green
    Write-Host ""
}

if (-not (Test-Path $BuildDir)) {
    New-Item -Path $BuildDir -ItemType Directory -Force | Out-Null
}

Write-Host "Building invoke-app.exe..." -ForegroundColor Yellow
Push-Location (Join-Path $ProjectRoot "cmd\invoke-app")
$env:GOOS = "windows"
$env:GOARCH = "amd64"
try {
    go build -ldflags "-H windowsgui" -o (Join-Path $BuildDir "invoke-app.exe") .
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build invoke-app.exe"
    }
    Write-Host "[OK] invoke-app.exe built successfully" -ForegroundColor Green
} finally {
    Pop-Location
}

Write-Host "Building invoke-server.exe (invoke)..." -ForegroundColor Yellow
Push-Location $ProjectRoot
try {
    go build -ldflags "-H windowsgui" -o (Join-Path $BuildDir "invoke-server.exe") .
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build invoke-server.exe"
    }
    Write-Host "[OK] invoke-server.exe built successfully" -ForegroundColor Green
} finally {
    Pop-Location
}

Write-Host "Copying scripts to build directory..." -ForegroundColor Yellow
$InvokeScriptPS = Join-Path $ProjectRoot "invoke.ps1"
$InvokeScriptSH = Join-Path $ProjectRoot "invoke.sh"
if (Test-Path $InvokeScriptPS) {
    Copy-Item $InvokeScriptPS -Destination $BuildDir -Force
    Write-Host "[OK] invoke.ps1 copied" -ForegroundColor Green
}
if (Test-Path $InvokeScriptSH) {
    Copy-Item $InvokeScriptSH -Destination $BuildDir -Force
    Write-Host "[OK] invoke.sh copied" -ForegroundColor Green
} else {
    Write-Host "[WARN] scripts not found, skipping" -ForegroundColor Yellow
}

if (-not $SkipMSI) {
    Write-Host ""
    Write-Host "Building MSI installer..." -ForegroundColor Yellow

    $WixPath = (Get-Command wix.exe -ErrorAction SilentlyContinue).Path
    if (-not $WixPath) {
        Write-Host "[WARN] WiX toolset not found in PATH" -ForegroundColor Yellow
        Write-Host "  Install WiX v4: dotnet tool install --global wix" -ForegroundColor Gray
        Write-Host "  Skipping MSI build" -ForegroundColor Yellow
    } else {
        $WxsFile = Join-Path $InstallerDir "invoke.wxs"
        $WixObjDir = Join-Path $BuildDir "wixobj"
        $MsiOutput = Join-Path $BuildDir "invoke-desktop-windows-x86.msi"

        if (-not (Test-Path $WixObjDir)) {
            New-Item -Path $WixObjDir -ItemType Directory -Force | Out-Null
        }

        Push-Location $ProjectRoot
        try {
            Write-Host "  Running WiX compiler for invoke-desktop-windows-x86.msi..." -ForegroundColor Gray
            & wix build -arch x64 -o $MsiOutput $WxsFile
            if ($LASTEXITCODE -ne 0) { throw "WiX build failed for invoke-desktop-windows-x86.msi" }
            Write-Host "[OK] MSI installer created: $MsiOutput" -ForegroundColor Green

            $SysWxsFile = Join-Path $InstallerDir "invoke-system.wxs"
            $SysMsiOutput = Join-Path $BuildDir "invoke-server-windows-x86.msi"
            Write-Host "  Running WiX compiler for invoke-server-windows-x86.msi..." -ForegroundColor Gray
            & wix build -ext WixToolset.UI.wixext -ext WixToolset.Firewall.wixext -arch x64 -o $SysMsiOutput $SysWxsFile
            if ($LASTEXITCODE -ne 0) { throw "WiX build failed for invoke-server-windows-x86.msi" }
            Write-Host "[OK] System MSI installer created: $SysMsiOutput" -ForegroundColor Green
        } finally {
            Pop-Location
        }
    }
}

Write-Host ""
Write-Host "Build completed successfully!" -ForegroundColor Green
Write-Host ""
Write-Host "Build artifacts:" -ForegroundColor Cyan
Get-ChildItem $BuildDir -File | ForEach-Object {
    $size = "{0:N2} MB" -f ($_.Length / 1MB)
    Write-Host "  $($_.Name) - $size" -ForegroundColor Gray
}
