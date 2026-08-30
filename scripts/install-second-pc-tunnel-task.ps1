[CmdletBinding()]
param(
    [string]$TaskName = 'DonutMarketBackendTunnel'
)

$ErrorActionPreference = 'Stop'
$supervisor = (Resolve-Path (Join-Path $PSScriptRoot 'second-pc-tunnel.ps1')).Path
$powerShell = (Get-Command powershell.exe -ErrorAction Stop).Source
$arguments = "-NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$supervisor`""
$action = New-ScheduledTaskAction -Execute $powerShell -Argument $arguments -WorkingDirectory (Split-Path $supervisor)
$logonTrigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$watchdogTrigger = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(1) `
    -RepetitionInterval (New-TimeSpan -Minutes 5) -RepetitionDuration (New-TimeSpan -Days 3650)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger @($logonTrigger, $watchdogTrigger) -Principal $principal `
    -Settings $settings -Description 'Keeps the loopback-only Donut market backend tunnel available.' -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "Installed and started scheduled task: $TaskName"
