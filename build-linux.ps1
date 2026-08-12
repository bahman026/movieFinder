# Builds the Linux binary inside Docker and drops it in .\dist
# Usage: .\build-linux.ps1

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "Building MovieFinder (Linux) in Docker..." -ForegroundColor Cyan
docker build --target export-linux --output "type=local,dest=$root\dist" $root
if ($LASTEXITCODE -ne 0) { throw "docker build failed with exit code $LASTEXITCODE" }

$bin = Join-Path $root 'dist\MovieFinder'
$size = [math]::Round((Get-Item $bin).Length / 1MB, 1)
Write-Host "Done: $bin ($size MB) - a Linux x86-64 binary" -ForegroundColor Green
