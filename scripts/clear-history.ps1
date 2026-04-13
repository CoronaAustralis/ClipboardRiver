param(
    [string]$ConfigPath,
    [switch]$Force
)

$root = Split-Path -Parent $PSScriptRoot

if (-not $Force) {
    $answer = Read-Host "This will delete all clipboard history rows and all files under the blob directory. Continue? [y/N]"
    if ($answer -notin @("y", "Y", "yes", "YES", "Yes")) {
        Write-Host "Cancelled."
        exit 1
    }
}

$previousConfig = $null
$hadPreviousConfig = $false
$exitCode = 0
if (Test-Path Env:CBR_CONFIG) {
    $previousConfig = $env:CBR_CONFIG
    $hadPreviousConfig = $true
}

try {
    if ($PSBoundParameters.ContainsKey("ConfigPath")) {
        $resolvedConfig = (Resolve-Path -LiteralPath $ConfigPath -ErrorAction Stop).Path
        $env:CBR_CONFIG = $resolvedConfig
    }

    Push-Location $root
    try {
        & (Join-Path $PSScriptRoot "go-local.ps1") run ./cmd/cb-river-maintenance clear-history
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
}
finally {
    if ($PSBoundParameters.ContainsKey("ConfigPath")) {
        if ($hadPreviousConfig) {
            $env:CBR_CONFIG = $previousConfig
        }
        else {
            Remove-Item Env:CBR_CONFIG -ErrorAction SilentlyContinue
        }
    }
}

exit $exitCode
