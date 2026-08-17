$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

New-Item -ItemType Directory -Force -Path 'dist' | Out-Null
go build -trimpath -ldflags '-s -w -H=windowsgui' -o 'dist\claude-patch.exe' '.\cmd\claude-patch'
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host '编译完成：dist\claude-patch.exe'
