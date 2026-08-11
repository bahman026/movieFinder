# Exports the HTTPS-intercepting root CAs from the Windows certificate store
# into .\certs, so the Docker build can verify TLS while a debugging proxy
# (Fiddler, Charles, Burp) or a corporate TLS-inspection appliance is running.
#
# Without this the build fails with:
#   x509: certificate signed by unknown authority
#
# Only needed once per machine, and only while such a proxy is active.
# Usage: .\export-proxy-ca.ps1
#
# Keep this file ASCII-only: Windows PowerShell 5.1 reads .ps1 as ANSI unless
# there is a BOM, and a stray UTF-8 dash breaks the parser.

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$outDir = Join-Path $root 'certs'
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# Subjects of the usual interceptors. Add your own CA here if it differs.
$pattern = 'Fiddler|DO_NOT_TRUST|Charles Proxy|PortSwigger|Burp|mitmproxy|ZScaler|NetSkope'

$certs = Get-ChildItem Cert:\CurrentUser\Root, Cert:\LocalMachine\Root |
    Where-Object { $_.Subject -match $pattern } |
    Sort-Object Thumbprint -Unique

if (-not $certs) {
    Write-Host "No interception CA found in the Windows root store - nothing to export." -ForegroundColor Yellow
    Write-Host "If the build still fails on TLS, add your CA subject to the pattern in this script."
    return
}

foreach ($cert in $certs) {
    $name = $cert.Subject -replace '^CN=', '' -replace ',.*$', ''
    $name = $name -replace '[^A-Za-z0-9._-]', '_'
    $path = Join-Path $outDir ($name + '.crt')

    # update-ca-certificates in the image wants one PEM certificate per file.
    $b64 = [Convert]::ToBase64String($cert.RawData, 'InsertLineBreaks')
    $pem = "-----BEGIN CERTIFICATE-----`n" + $b64 + "`n-----END CERTIFICATE-----`n"
    Set-Content -Path $path -Value $pem -Encoding ascii -NoNewline

    $expires = $cert.NotAfter.ToString('yyyy-MM-dd')
    Write-Host ("Exported " + $cert.Subject) -ForegroundColor Green
    Write-Host ("      -> " + $path + " (expires " + $expires + ")")
}

Write-Host ""
Write-Host "Run .\build.ps1 again - the image will now trust these CAs." -ForegroundColor Cyan
