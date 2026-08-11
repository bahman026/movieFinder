# Builds MovieFinder.exe inside Docker and drops it in .\dist
# Usage: .\build.ps1

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "Building MovieFinder.exe in Docker..." -ForegroundColor Cyan
docker build --target export --output "type=local,dest=$root\dist" $root
if ($LASTEXITCODE -ne 0) { throw "docker build failed with exit code $LASTEXITCODE" }

# The build resolves dependencies inside the image; bring the lockfile back so
# later builds are reproducible.
$distSum = Join-Path $root 'dist\go.sum'
$rootSum = Join-Path $root 'go.sum'
if (Test-Path $distSum) {
    Copy-Item $distSum $rootSum -Force
    Remove-Item $distSum -Force
}

$exe = Join-Path $root 'dist\MovieFinder.exe'
$size = [math]::Round((Get-Item $exe).Length / 1MB, 1)
Write-Host "Done: $exe ($size MB)" -ForegroundColor Green
