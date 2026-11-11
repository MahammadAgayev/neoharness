# Builds neoharness and installs it to $env:BIN (default ~\.local\bin).
$ErrorActionPreference = 'Stop'

$bin = if ($env:BIN) { $env:BIN } else { Join-Path $HOME '.local\bin' }
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force $bin | Out-Null
go build -o (Join-Path $bin 'neoharness.exe') ./cmd/neoharness
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Output "installed $(Join-Path $bin 'neoharness.exe')"
