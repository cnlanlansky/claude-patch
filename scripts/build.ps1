$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)

New-Item -ItemType Directory -Force -Path 'dist' | Out-Null

$version = $env:CLAUDE_PATCH_VERSION
if ([string]::IsNullOrWhiteSpace($version)) {
    $tag = $null
    if (Get-Command git -ErrorAction SilentlyContinue) {
        $tag = (& git tag --points-at HEAD | Where-Object { $_ -match '^v[0-9]+\.[0-9]+\.[0-9]+$' } | Select-Object -First 1)
    }
    if ($null -ne $tag) {
        $version = ([string]$tag).Trim()
    }
}
if ([string]::IsNullOrWhiteSpace($version) -or ($version -ne 'dev' -and $version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$')) {
    $version = 'dev'
}
$ldflags = "-s -w -H=windowsgui -X github.com/cnlanlansky/claude-patch/internal/version.Current=$version"
go build -trimpath -ldflags $ldflags -o 'dist\claude-patch.exe' '.\cmd\claude-patch'
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host 'Build complete: dist\claude-patch.exe'
