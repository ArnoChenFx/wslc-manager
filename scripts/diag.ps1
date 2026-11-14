# WSLC Manager diagnostic script
#
# Purpose: when I (or any agent) need to inspect the UI layout, one command
# builds with the automation plugin, starts the app, dumps every element with
# its real DIP coordinates, and optionally screenshots. No manual clicking,
# no occlusion by other windows, no repeated "please send a screenshot".
#
# Usage:
#   .\scripts\diag.ps1
#   .\scripts\diag.ps1 -Page system
#   .\scripts\diag.ps1 -Page compose -Shot compose.png
#   .\scripts\diag.ps1 -Report          # full Markdown diagnostic report
#
# Messages are ASCII on purpose: Windows PowerShell 5.1 renders CJK in
# scripts with the console code page and garbles them.

[CmdletBinding()]
param(
    # Page to show after start: containers|images|volumes|networks|compose|system
    [string]$Page = '',

    # Seconds to wait for background data before dumping, default 10
    [int]$Wait = 10,

    # Screenshot file name (relative to -OutDir); empty skips it
    [string]$Shot = '',

    # Output directory
    [string]$OutDir = '.diag',

    # Also produce the full Markdown diagnostic report the app itself exports
    [switch]$Report
)

$ErrorActionPreference = 'Stop'

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
    $exe = Join-Path $OutDir 'wslc-manager-e2e.exe'
    $sock = Join-Path $env:TEMP 'wslc-manager-diag.sock'

    Write-Host '[1/5] build with automation plugin ...' -ForegroundColor Cyan
    & go build -tags e2e -o $exe .
    if ($LASTEXITCODE -ne 0) { throw 'build failed' }

    Write-Host '[2/5] ensure mygo-automation ...' -ForegroundColor Cyan
    $auto = Get-Command mygo-automation -ErrorAction SilentlyContinue
    if (-not $auto) {
        & go install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation
        $autoPath = Join-Path (go env GOPATH) 'bin\mygo-automation.exe'
        if (-not (Test-Path $autoPath)) { throw 'installing mygo-automation failed' }
        $autoPathResolved = $autoPath
    } else {
        $autoPathResolved = $auto.Source
    }

    Write-Host '[3/5] start app ...' -ForegroundColor Cyan
    if (Test-Path $sock) { Remove-Item $sock -Force }
    $env:MYGO_AUTOMATION = $sock
    if ($Page) { $env:WSLC_MANAGER_SHOT_PAGE = $Page }
    $proc = Start-Process -FilePath $exe -PassThru

    try {
        $deadline = (Get-Date).AddSeconds(30)
        while (-not (Test-Path $sock)) {
            if ((Get-Date) -gt $deadline) { throw 'timed out waiting for automation socket' }
            Start-Sleep -Milliseconds 200
        }

        Write-Host "[4/5] wait $Wait s for data ..." -ForegroundColor Cyan
        Start-Sleep -Seconds $Wait

        Write-Host ''
        Write-Host '=== element tree (with bounds) ===' -ForegroundColor Green
        # Go talks to the socket itself: PowerShell truncates long lines and
        # adds a BOM, which corrupts the multi-KB single-line JSON snapshot.
        $treePath = Join-Path $OutDir 'ui-tree.txt'
        & go run ./cmd/diagtree -socket $sock -out $treePath
        if ($LASTEXITCODE -ne 0) {
            Write-Host 'dumping the tree failed' -ForegroundColor Red
        }

        if ($Report) {
            Write-Host ''
            Write-Host '=== markdown report ===' -ForegroundColor Green
            $reportPath = Join-Path $OutDir 'report.md'
            & go run ./cmd/diagtree -report $reportPath
            if ($LASTEXITCODE -eq 0 -and (Test-Path $reportPath)) {
                Write-Host "report: $reportPath" -ForegroundColor DarkGray
            }
        }

        if ($Shot) {
            $shotPath = Join-Path $OutDir $Shot
            Write-Host ''
            Write-Host "[5/5] screenshot -> $shotPath" -ForegroundColor Cyan
            $session = 'wslc-diag'
            & $autoPathResolved open --socket $sock --session $session | Out-Null
            try {
                & $autoPathResolved screenshot $shotPath --session $session
            } finally {
                & $autoPathResolved close --session $session 2>$null | Out-Null
            }
        } else {
            Write-Host '[5/5] skip screenshot' -ForegroundColor DarkGray
        }
    } finally {
        if ($proc -and -not $proc.HasExited) {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
    }

    Write-Host ''
    Write-Host 'done.' -ForegroundColor Green
} finally {
    Pop-Location
}
