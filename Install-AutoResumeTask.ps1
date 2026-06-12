<#
.SYNOPSIS
    Installs (or removes) a Windows Scheduled Task that runs Claude Auto-Resume
    in watch mode in the background, starting automatically when you log on.

.EXAMPLE
    .\Install-AutoResumeTask.ps1
    Registers the task and starts it immediately.

.EXAMPLE
    .\Install-AutoResumeTask.ps1 -Uninstall
    Stops and removes the task.
#>
[CmdletBinding()]
param(
    [string]$TaskName = 'ClaudeAutoResume',
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'

if ($Uninstall) {
    $existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    if ($existing) {
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
        Write-Host "Removed scheduled task '$TaskName'."
    } else {
        Write-Host "Scheduled task '$TaskName' is not installed."
    }
    return
}

$scriptPath = Join-Path $PSScriptRoot 'Claude-AutoResume.ps1'
if (-not (Test-Path $scriptPath)) {
    throw "Cannot find Claude-AutoResume.ps1 next to this installer ($scriptPath)."
}

$action = New-ScheduledTaskAction -Execute 'powershell.exe' `
    -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$scriptPath`" -Watch"

$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 5) `
    -ExecutionTimeLimit ([TimeSpan]::Zero)

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Settings $settings -Description 'Auto-resumes Claude Code sessions when the usage limit resets.' -Force | Out-Null

Start-ScheduledTask -TaskName $TaskName

Write-Host "Installed and started scheduled task '$TaskName'."
Write-Host "It runs hidden at every logon. Logs: $env:LOCALAPPDATA\ClaudeAutoResume\auto-resume.log"
Write-Host "To remove it later: .\Install-AutoResumeTask.ps1 -Uninstall"
