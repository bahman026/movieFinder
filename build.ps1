# Builds MovieFinder.exe inside Docker and drops it in .\dist
# Usage: .\build.ps1

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "Building MovieFinder.exe in Docker..." -ForegroundColor Cyan
docker build --target export --output "type=local,dest=$root\dist" $root
if ($LASTEXITCODE -ne 0) { throw "docker build failed with exit code $LASTEXITCODE" }

# The build resolves dependencies inside the image; bring go.mod and go.sum back
# so the repo can be built without going through Docker first.
foreach ($name in 'go.mod', 'go.sum') {
    $fromDist = Join-Path $root "dist\$name"
    if (Test-Path $fromDist) {
        Copy-Item $fromDist (Join-Path $root $name) -Force
        Remove-Item $fromDist -Force
    }
}

$exe = Join-Path $root 'dist\MovieFinder.exe'
$size = [math]::Round((Get-Item $exe).Length / 1MB, 1)
Write-Host "Done: $exe ($size MB)" -ForegroundColor Green
