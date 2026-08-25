[CmdletBinding()]
param(
    [string]$RemoteHost = '192.168.137.2',
    [string]$RemoteUser = 'jayzdayz',
    [string]$IdentityFile = "$env:USERPROFILE\.ssh\clanskiller_secondpc_ed25519"
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $IdentityFile)) {
    throw "SSH identity file not found: $IdentityFile"
}

Write-Host 'Forwarding local http://127.0.0.1:8080 to the second-PC backend.'
Write-Host 'Keep this window open while using the dashboard or Fabric client. Press Ctrl+C to stop.'
& ssh -i $IdentityFile -o BatchMode=yes -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -N -L '127.0.0.1:8080:127.0.0.1:8080' "$RemoteUser@$RemoteHost"
if ($LASTEXITCODE -ne 0) {
    throw "SSH tunnel exited with code $LASTEXITCODE"
}
