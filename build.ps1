param(
    [switch]$SkipMSI,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"

$ProjectRoot = $PSScriptRoot
$BuildDir = Join-Path $ProjectRoot "build"
$InstallerDir = Join-Path $ProjectRoot "installer"

Write-Host "PowerTerm Build Script" -ForegroundColor Cyan
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
try {
    go build -o (Join-Path $BuildDir "invoke-app.exe") .
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build invoke-app.exe"
    }
    Write-Host "✓ invoke-app.exe built successfully" -ForegroundColor Green
} finally {
    Pop-Location
}

Write-Host "Building invoke-server.exe (powerterm)..." -ForegroundColor Yellow
Push-Location $ProjectRoot
try {
    go build -o (Join-Path $BuildDir "invoke-server.exe") .
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build invoke-server.exe"
    }
    Write-Host "✓ invoke-server.exe built successfully" -ForegroundColor Green
} finally {
    Pop-Location
}

Write-Host "Copying invoke.ps1 to build directory..." -ForegroundColor Yellow
$InvokeScript = Join-Path $ProjectRoot "invoke.ps1"
if (Test-Path $InvokeScript) {
    Copy-Item $InvokeScript -Destination $BuildDir -Force
    Write-Host "✓ invoke.ps1 copied" -ForegroundColor Green
} else {
    Write-Host "⚠ invoke.ps1 not found, skipping" -ForegroundColor Yellow
}

if (-not $SkipMSI) {
    Write-Host ""
    Write-Host "Building MSI installer..." -ForegroundColor Yellow

    $WixPath = (Get-Command wix.exe -ErrorAction SilentlyContinue).Path
    if (-not $WixPath) {
        Write-Host "⚠ WiX toolset not found in PATH" -ForegroundColor Yellow
        Write-Host "  Install WiX v4: dotnet tool install --global wix" -ForegroundColor Gray
        Write-Host "  Skipping MSI build" -ForegroundColor Yellow
    } else {
        $WxsFile = Join-Path $InstallerDir "invoke.wxs"
        $WixObjDir = Join-Path $BuildDir "wixobj"
        $MsiOutput = Join-Path $BuildDir "invoke-setup.msi"

        if (-not (Test-Path $WixObjDir)) {
            New-Item -Path $WixObjDir -ItemType Directory -Force | Out-Null
        }

        Push-Location $ProjectRoot
        try {
            Write-Host "  Running WiX compiler..." -ForegroundColor Gray
            & wix build -arch x64 -o $MsiOutput $WxsFile

            if ($LASTEXITCODE -ne 0) {
                throw "WiX build failed"
            }

            Write-Host "✓ MSI installer created: $MsiOutput" -ForegroundColor Green
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
