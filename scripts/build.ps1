# SPDX-License-Identifier: GPL-3.0-or-later
param([string]$TargetOS, [string]$TargetArch)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location -LiteralPath $projectRoot
$priorOS=$env:GOOS; $priorArch=$env:GOARCH; $priorCGO=$env:CGO_ENABLED
try {
    if (-not $TargetOS) { $TargetOS=(& go env GOOS).Trim() }
    if (-not $TargetArch) { $TargetArch=(& go env GOARCH).Trim() }
    if ($TargetOS -notmatch '^[a-z0-9]+$' -or $TargetArch -notmatch '^[a-z0-9]+$') { throw 'Invalid target OS/architecture' }
    $env:GOOS=$TargetOS; $env:GOARCH=$TargetArch; $env:CGO_ENABLED='0'
    $outputRoot=Join-Path $projectRoot "_local/bin/$TargetOS-$TargetArch"
    New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
    $suffix=if ($TargetOS -eq 'windows') {'.exe'} else {''}
    foreach ($app in @('mms-client','mms-server','goose-publisher','goose-subscriber','sv-publisher','sv-subscriber','scdinspect')) {
        $name=$app.Replace('/','-')+$suffix
        & go build -trimpath -o (Join-Path $outputRoot $name) "./cmd/$app"
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $app" }
        Write-Output (Join-Path $outputRoot $name)
    }
} finally {
    $env:GOOS=$priorOS; $env:GOARCH=$priorArch; $env:CGO_ENABLED=$priorCGO
    Pop-Location
}
