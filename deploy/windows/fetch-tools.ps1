# Downloads the embeddable PostgreSQL Windows build and stages it under
# deploy/windows/pgsql/ so build-bundle.sh can pack it into the installer.
#
# PgVersion's major must match the server the project runs elsewhere
# (compose.yaml: postgres:18.4-alpine) so the bundled desktop cluster and the
# Docker deployment stay on the same PostgreSQL major.
#
#   pwsh deploy/windows/fetch-tools.ps1 [-PgVersion 18.4-1]
param(
  [string]$PgVersion = "18.4-1"
)
$ErrorActionPreference = "Stop"

$root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$dest = Join-Path $PSScriptRoot "pgsql"
$tmp  = Join-Path $env:TEMP "varyaone-pgsql.zip"

# EnterpriseDB binary-only zip (no installer service, portable).
$url = "https://get.enterprisedb.com/postgresql/postgresql-$PgVersion-windows-x64-binaries.zip"

Write-Host ">> downloading $url"
Invoke-WebRequest -Uri $url -OutFile $tmp

Write-Host ">> extracting"
if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
$extract = Join-Path $env:TEMP "varyaone-pgsql"
if (Test-Path $extract) { Remove-Item -Recurse -Force $extract }
Expand-Archive -Path $tmp -DestinationPath $extract

# The zip contains a top-level pgsql/ directory with bin/ lib/ share/.
Move-Item (Join-Path $extract "pgsql") $dest
Remove-Item -Recurse -Force $extract
Remove-Item $tmp

Write-Host ">> staged $dest"

# The EnterpriseDB binary zip is NOT self-contained: initdb.exe / postgres.exe are
# linked against the MSVC runtime and fail with 0xC0000135 (DLL not found) on a
# clean Windows box. Ship the VC++ redistributable so the installer can install
# it as a prerequisite.
$vcRedist = Join-Path $PSScriptRoot "vc_redist.x64.exe"
Write-Host ">> downloading vc_redist.x64.exe"
Invoke-WebRequest -Uri "https://aka.ms/vs/17/release/vc_redist.x64.exe" -OutFile $vcRedist
Write-Host ">> staged $vcRedist"

# WebView2 Evergreen bootstrapper — varyaone-client.exe needs the WebView2
# runtime, which is absent on Windows Server / LTSC / older Win10. The
# bootstrapper is ~2 MB and no-ops if a runtime is already present.
$wv2 = Join-Path $PSScriptRoot "MicrosoftEdgeWebview2Setup.exe"
Write-Host ">> downloading MicrosoftEdgeWebview2Setup.exe"
Invoke-WebRequest -Uri "https://go.microsoft.com/fwlink/p/?LinkId=2124703" -OutFile $wv2
Write-Host ">> staged $wv2"
