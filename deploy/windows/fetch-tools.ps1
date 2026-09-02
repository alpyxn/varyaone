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
