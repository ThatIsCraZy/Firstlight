param(
    [string[]]$Packages = @("./..."),
    [string[]]$GoArgs = @()
)

$ErrorActionPreference = "Stop"

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $root

$cache = Join-Path $root ".gocache"
$tmp = Join-Path $root ".gotmp"
New-Item -ItemType Directory -Force $cache, $tmp | Out-Null

$env:GOCACHE = (Resolve-Path $cache).Path
$env:GOTMPDIR = (Resolve-Path $tmp).Path
$env:TEMP = $env:GOTMPDIR
$env:TMP = $env:GOTMPDIR

go test @GoArgs @Packages
