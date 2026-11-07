# SPDX-License-Identifier: GPL-3.0-or-later
param([switch]$Race, [switch]$Examples)
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
Push-Location -LiteralPath $projectRoot
try {
    $results=Join-Path $projectRoot '_local/reports/unit'
    New-Item -ItemType Directory -Path $results -Force | Out-Null
    $testArguments=@('test','-json','-count=1','-timeout=120s')
    if ($Race) { $testArguments+='-race' }
    $testArguments += './...'
    & go @testArguments | Tee-Object -FilePath (Join-Path $results 'go-tests.jsonl')
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    if ($Examples) {
        foreach ($example in @('mms/read-write','mms/read-batch','mms/browse','mms/named-list','mms/report','goose/publish-subscribe','sv/publish-subscribe','sv/data-layout','scd/breaker','ied/control-report','sv/scl')) {
            $label=$example.Replace('/','-')
            & go run "./examples/$example" | Tee-Object -FilePath (Join-Path $results "$label.log")
            if ($LASTEXITCODE -ne 0) { throw "$example failed" }
        }
    }
    Write-Output 'Go tests and static checks passed.'
} finally { Pop-Location }
