[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repository = Split-Path -Parent $PSScriptRoot
$credentialFile = Join-Path $repository 'data\donut-api-key.dpapi'
$apiKey = [Environment]::GetEnvironmentVariable('DONUT_API_KEY', 'Process')

try {
    $existing = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/api/v1/flips' -TimeoutSec 2
    if ($null -ne $existing.version -and $null -ne $existing.status) {
        Write-Host 'Donut backend is already running at http://127.0.0.1:8080'
        return
    }
} catch {
    # No compatible local backend is listening; continue with normal startup.
}

if ([string]::IsNullOrWhiteSpace($apiKey)) {
    if (Test-Path -LiteralPath $credentialFile) {
        $secureKey = (Get-Content -LiteralPath $credentialFile -Raw).Trim() | ConvertTo-SecureString
    } else {
        $secureKey = Read-Host 'Donut API key (stored encrypted for this Windows user)' -AsSecureString
        New-Item -ItemType Directory -Path (Split-Path -Parent $credentialFile) -Force | Out-Null
        $secureKey | ConvertFrom-SecureString | Set-Content -LiteralPath $credentialFile -Encoding utf8
    }

    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureKey)
    try {
        $apiKey = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

if ([string]::IsNullOrWhiteSpace($apiKey)) {
    throw 'The Donut API key cannot be empty.'
}

$env:DONUT_API_KEY = $apiKey
Set-Location -LiteralPath $repository
Write-Host 'Starting Donut backend at http://127.0.0.1:8080'
Write-Host 'Keep this window open. Press Ctrl+C to stop.'
go run ./cmd/server
