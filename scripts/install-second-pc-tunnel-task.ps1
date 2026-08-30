[CmdletBinding()]
param(
    [string]$TaskName = 'DonutMarketBackendTunnel'
)

$ErrorActionPreference = 'Stop'
$supervisor = (Resolve-Path (Join-Path $PSScriptRoot 'second-pc-tunnel.ps1')).Path
$powerShell = (Get-Command powershell.exe -ErrorAction Stop).Source
$arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$supervisor`""
$action = New-ScheduledTaskAction -Execute $powerShell -Argument $arguments -WorkingDirectory (Split-Path $supervisor)
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal `
    -Settings $settings -Description 'Keeps the loopback-only Donut market backend tunnel available.' -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started scheduled task: $TaskName"
