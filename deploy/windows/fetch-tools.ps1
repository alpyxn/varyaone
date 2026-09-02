# Downloads the embeddable PostgreSQL Windows build + the runtime prerequisites
# and stages them under deploy/windows/ so build-bundle.sh can pack them.
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
# Invoke-WebRequest is ~10x slower on Windows PowerShell while it paints a
# progress bar; turn it off for the large PostgreSQL download.
$ProgressPreference = "SilentlyContinue"

$dest = Join-Path $PSScriptRoot "pgsql"
$tmp  = Join-Path $env:TEMP "varyaone-pgsql.zip"

function Get-File($Uri, $OutFile, $MinBytes) {
  Write-Host ">> downloading $Uri"
  Invoke-WebRequest -Uri $Uri -OutFile $OutFile
  $len = (Get-Item $OutFile).Length
  if ($len -lt $MinBytes) {
    throw "download of $Uri is only $len bytes (expected >= $MinBytes) — got an error page or a truncated response?"
  }
  Write-Host ("   {0:N1} MB" -f ($len / 1MB))
}

# --- PostgreSQL (EnterpriseDB binary-only zip, no service, portable) ----------
$url = "https://get.enterprisedb.com/postgresql/postgresql-$PgVersion-windows-x64-binaries.zip"
Get-File $url $tmp (100MB)

Write-Host ">> extracting"
if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
$extract = Join-Path $env:TEMP "varyaone-pgsql"
if (Test-Path $extract) { Remove-Item -Recurse -Force $extract }
New-Item -ItemType Directory -Path $extract | Out-Null

# NOT Expand-Archive: on Windows PowerShell it has silently written partial /
# truncated files out of large deep archives like this one (~4000 entries) — the
# symptom is a 241 KB initdb.exe that then fails to load with 0xC0000135. bsdtar
# (built into Win10 1803+ / Server 2019+) is reliable; .NET ZipFile is the
# fallback.
$tarExe = Get-Command tar.exe -ErrorAction SilentlyContinue
if ($tarExe) {
  & tar.exe -xf $tmp -C $extract
  if ($LASTEXITCODE -ne 0) { throw "tar extraction failed (exit $LASTEXITCODE)" }
} else {
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  [System.IO.Compression.ZipFile]::ExtractToDirectory($tmp, $extract)
}

# The zip contains a top-level pgsql/ directory with bin/ lib/ share/.
Move-Item (Join-Path $extract "pgsql") $dest
Remove-Item -Recurse -Force $extract
Remove-Item $tmp

# --- integrity gate ---------------------------------------------------------
# Fail here in CI, never at runtime on a user's machine. Check by SIZE, not just
# existence: a truncated extraction leaves the file present but short.
$bin = Join-Path $dest "bin"
$minSize = @{
  "initdb.exe"   = 400KB
  "postgres.exe" = 8MB
  "pg_ctl.exe"   = 200KB
  "psql.exe"     = 400KB
}
foreach ($name in $minSize.Keys) {
  $f = Join-Path $bin $name
  if (-not (Test-Path $f)) { throw "PostgreSQL bundle incomplete: $name missing" }
  $sz = (Get-Item $f).Length
  if ($sz -lt $minSize[$name]) {
    throw "PostgreSQL bundle truncated: $name is $sz bytes (expected >= $($minSize[$name])) — extraction dropped data"
  }
}
$dllCount = (Get-ChildItem $bin -Filter *.dll -ErrorAction SilentlyContinue).Count
if ($dllCount -lt 25) {
  throw "PostgreSQL bundle incomplete: only $dllCount DLLs in bin\ (expected 30+)"
}
# initdb/postgres load these at startup; a missing one is the classic 0xC0000135.
foreach ($pat in @("libintl*.dll", "libpq*.dll", "libcrypto*.dll", "libssl*.dll")) {
  if (-not (Get-ChildItem $bin -Filter $pat -ErrorAction SilentlyContinue)) {
    throw "PostgreSQL bundle incomplete: no $pat in bin\"
  }
}
Write-Host ">> staged $dest ($dllCount DLLs, initdb.exe $((Get-Item (Join-Path $bin 'initdb.exe')).Length) bytes)"

# --- runtime prerequisites -------------------------------------------------
# EDB binaries are MSVC-linked; without the VC++ runtime initdb fails 0xC0000135
# on a clean box. installer.iss installs this before registering the service.
Get-File "https://aka.ms/vs/17/release/vc_redist.x64.exe" `
  (Join-Path $PSScriptRoot "vc_redist.x64.exe") (10MB)

# WebView2 Evergreen bootstrapper for varyaone-client.exe (absent on Server /
# LTSC / older Win10). ~2 MB; no-ops if a runtime is already present.
Get-File "https://go.microsoft.com/fwlink/p/?LinkId=2124703" `
  (Join-Path $PSScriptRoot "MicrosoftEdgeWebview2Setup.exe") (1MB)

Write-Host ">> all prerequisites staged"
