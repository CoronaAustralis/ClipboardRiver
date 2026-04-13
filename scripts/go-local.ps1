param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$GoArgs
)

if (-not $GoArgs -or $GoArgs.Count -eq 0) {
    Write-Error "Usage: .\\scripts\\go-local.ps1 <go subcommand> [args...]"
    exit 1
}

$root = Split-Path -Parent $PSScriptRoot
$env:GOMODCACHE = Join-Path $root ".gomodcache"
$env:GOCACHE = Join-Path $root ".gocache"
$env:GOFLAGS = (($env:GOFLAGS, "-modcacherw") | Where-Object { $_ -and $_.Trim() -ne "" }) -join " "

if (Test-Path -LiteralPath $env:GOMODCACHE) {
    Get-ChildItem -LiteralPath $env:GOMODCACHE -Force -Recurse -ErrorAction SilentlyContinue | ForEach-Object {
        if ($_.Attributes -band [IO.FileAttributes]::ReadOnly) {
            $_.Attributes = $_.Attributes -band (-bnot [IO.FileAttributes]::ReadOnly)
        }
    }
}

& go @GoArgs
exit $LASTEXITCODE
