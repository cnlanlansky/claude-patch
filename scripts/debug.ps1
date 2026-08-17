$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
go build -o 'dist\claude-patch.exe' '.\cmd\claude-patch'
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& '.\dist\claude-patch.exe' @args
