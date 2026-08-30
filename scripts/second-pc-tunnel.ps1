[CmdletBinding()]
param(
    [string]$RemoteHost = '192.168.137.2',
    [string]$RemoteUser = 'jayzdayz',
    [string]$IdentityFile = "$env:USERPROFILE\.ssh\clanskiller_secondpc_ed25519",
    [ValidateRange(2, 60)]
    [int]$HealthIntervalSeconds = 5,
    [switch]$Once
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $IdentityFile)) {
    throw "SSH identity file not found: $IdentityFile"
}

function Test-DonutBackend {
    try {
        $health = Invoke-RestMethod -Uri 'http://127.0.0.1:8080/healthz' -TimeoutSec 2
        return $health.status -eq 'ready'
    } catch {
        return $false
    }
}

$failures = 0
Write-Host 'Supervising local http://127.0.0.1:8080 access to the second-PC backend.'
while ($true) {
    if (Test-DonutBackend) {
        $failures = 0
        if ($Once) { return }
        Start-Sleep -Seconds $HealthIntervalSeconds
        continue
    }

    Write-Host 'Backend is unavailable locally; starting the authenticated SSH forward.'
    & ssh -i $IdentityFile -o BatchMode=yes -o ConnectTimeout=8 -o ExitOnForwardFailure=yes `
        -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -N `
        -L '127.0.0.1:8080:127.0.0.1:8080' "$RemoteUser@$RemoteHost"
    $exitCode = $LASTEXITCODE
    if ($Once) {
        throw "SSH tunnel exited with code $exitCode"
    }
    $failures++
    $retrySeconds = [Math]::Min(30, [Math]::Pow(2, [Math]::Min(5, $failures - 1)))
    Write-Warning "SSH tunnel exited with code $exitCode; retrying in $retrySeconds seconds."
    Start-Sleep -Seconds $retrySeconds
}
