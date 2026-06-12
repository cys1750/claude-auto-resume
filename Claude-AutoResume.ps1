<#
.SYNOPSIS
    Auto-resumes Claude Code sessions after the 5-hour usage-limit window resets.

.DESCRIPTION
    Detects when your Claude Code usage limit has been hit, figures out when the
    limit window resets, waits for the reset, and then resumes the sessions that
    were active when you got cut off.

    Works with sessions started from the CLI or the Claude Code desktop app
    (both store transcripts under the same per-user session directories).

.PARAMETER Watch
    Run forever: periodically probe for the usage limit and auto-resume whenever
    a reset happens. This is the mode the Scheduled Task installer uses.

.PARAMETER Force
    Skip limit detection and immediately resume the most recent sessions.

.PARAMETER Prompt
    Override the prompt sent to each resumed session
    (default: "Continue working on the previous task from where you left off.").

.PARAMETER ConfigPath
    Path to a config.json (defaults to config.json next to this script).

.EXAMPLE
    .\Claude-AutoResume.ps1
    One-shot: if the limit is active, wait for reset and resume; otherwise exit.

.EXAMPLE
    .\Claude-AutoResume.ps1 -Watch
    Keep running in the background and auto-resume every time the limit resets.
#>
[CmdletBinding()]
param(
    [switch]$Watch,
    [switch]$Force,
    [string]$Prompt,
    [string]$ConfigPath
)

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Paths and state
# ---------------------------------------------------------------------------
$script:DataDir = Join-Path $env:LOCALAPPDATA 'ClaudeAutoResume'
$script:LogFile = Join-Path $script:DataDir 'auto-resume.log'
$script:PendingFile = Join-Path $script:DataDir 'pending.json'
$script:ProbeDir = Join-Path $script:DataDir 'probe'
New-Item -ItemType Directory -Path $script:DataDir, $script:ProbeDir -Force | Out-Null

function Write-Log {
    param([string]$Message)
    $line = "[{0:yyyy-MM-dd HH:mm:ss}] {1}" -f (Get-Date), $Message
    Write-Host $line
    Add-Content -Path $script:LogFile -Value $line
}

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
function Get-Config {
    $config = @{
        # Prompt sent to each resumed session in headless mode.
        Prompt               = 'Continue working on the previous task from where you left off. If the previous task is already complete, reply with a short status summary and stop.'
        # How far back (hours) to look for sessions to resume when a limit is detected.
        LookbackHours        = 5
        # Resume at most this many sessions (most recent first).
        MaxSessionsPerRun    = 3
        # 'headless' = send the prompt via `claude -p --resume` (fully unattended).
        # 'interactive' = open a terminal window per session with `claude --resume`
        #                 so you can take over (and use /remote-control from your phone).
        Mode                 = 'headless'
        # Minutes between limit probes in -Watch mode.
        CheckIntervalMinutes = 15
        # Extra minutes to wait past the reported reset time before resuming.
        ResetBufferMinutes   = 3
        # Minutes to wait between re-probes when the reset time could not be parsed.
        UnknownResetRetryMinutes = 30
        # Cheap model used for the limit probe.
        ProbeModel           = 'haiku'
        # Full path to the claude executable; leave empty to use PATH.
        ClaudePath           = ''
        # Extra arguments appended to headless resume calls, e.g.
        # ["--permission-mode", "acceptEdits"] so the agent isn't blocked on file edits.
        ExtraArgs            = @()
        # Optional list of project paths to restrict resuming to (empty = all).
        Projects             = @()
    }

    $path = $ConfigPath
    if (-not $path) { $path = Join-Path $PSScriptRoot 'config.json' }
    if (Test-Path $path) {
        try {
            $userConfig = Get-Content $path -Raw | ConvertFrom-Json
            foreach ($prop in $userConfig.PSObject.Properties) {
                $config[$prop.Name] = $prop.Value
            }
            Write-Log "Loaded config from $path"
        } catch {
            Write-Log "WARNING: Failed to parse $path ($($_.Exception.Message)); using defaults."
        }
    }
    if ($Prompt) { $config.Prompt = $Prompt }
    return $config
}

function Get-ClaudeCommand {
    if ($script:Config.ClaudePath) {
        if (Test-Path $script:Config.ClaudePath) { return $script:Config.ClaudePath }
        throw "ClaudePath '$($script:Config.ClaudePath)' from config does not exist."
    }
    $cmd = Get-Command claude -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    throw "Could not find 'claude' on PATH. Install Claude Code CLI or set ClaudePath in config.json."
}

# ---------------------------------------------------------------------------
# Running claude
# ---------------------------------------------------------------------------
function Invoke-Claude {
    param(
        [string[]]$Arguments,
        [string]$WorkingDirectory
    )
    $prevLocation = Get-Location
    $prevEap = $ErrorActionPreference
    try {
        if ($WorkingDirectory) { Set-Location $WorkingDirectory }
        # Native stderr lines become ErrorRecords under Stop; relax while invoking.
        $ErrorActionPreference = 'Continue'
        $raw = & $script:ClaudeCmd @Arguments 2>&1
        $exitCode = $LASTEXITCODE
        $text = ($raw | ForEach-Object { "$_" }) -join "`n"
        return @{ Output = $text; ExitCode = $exitCode }
    } finally {
        $ErrorActionPreference = $prevEap
        Set-Location $prevLocation
    }
}

# ---------------------------------------------------------------------------
# Limit detection
# ---------------------------------------------------------------------------
$script:DayMap = @{
    Mon = [DayOfWeek]::Monday;    Tue = [DayOfWeek]::Tuesday
    Wed = [DayOfWeek]::Wednesday; Thu = [DayOfWeek]::Thursday
    Fri = [DayOfWeek]::Friday;    Sat = [DayOfWeek]::Saturday
    Sun = [DayOfWeek]::Sunday
}

function Get-ResetDateTime {
    param([string]$Day, [int]$Hour, [int]$Minute, [string]$AmPm)
    $h = $Hour % 12
    if ($AmPm -ieq 'pm') { $h += 12 }
    $dt = (Get-Date).Date.AddHours($h).AddMinutes($Minute)
    if ($Day -and $script:DayMap.ContainsKey($Day)) {
        $target = $script:DayMap[$Day]
        while ($dt.DayOfWeek -ne $target -or $dt -le (Get-Date)) { $dt = $dt.AddDays(1) }
    } elseif ($dt -le (Get-Date)) {
        $dt = $dt.AddDays(1)
    }
    return $dt
}

function Get-LimitInfo {
    # Parses claude output. Returns $null when no limit message is present,
    # otherwise @{ Limited = $true; ResetTime = [datetime] or $null }.
    param([string]$Text)
    if (-not $Text) { return $null }

    # Older machine-parseable form: "Claude AI usage limit reached|<unix-epoch>"
    if ($Text -match 'usage limit reached\|(\d{9,12})') {
        $reset = [DateTimeOffset]::FromUnixTimeSeconds([long]$Matches[1]).LocalDateTime
        return @{ Limited = $true; ResetTime = $reset }
    }

    # Human-readable forms, e.g.:
    #   "You've hit your session limit · resets 3:45pm"
    #   "You've hit your weekly limit · resets Mon 12:00am"
    #   "Claude usage limit reached. Your limit will reset at 7pm"
    if ($Text -match '(?i)(hit your [^\r\n]*?limit|usage limit reached|session limit reached|rate limit)') {
        $reset = $null
        if ($Text -match '(?i)reset(?:s)?(?:\s+at)?\s+(?:(Mon|Tue|Wed|Thu|Fri|Sat|Sun)[a-z]*\s+)?(\d{1,2})(?::(\d{2}))?\s*([ap]m)') {
            $minute = 0
            if ($Matches[3]) { $minute = [int]$Matches[3] }
            $reset = Get-ResetDateTime -Day $Matches[1] -Hour ([int]$Matches[2]) -Minute $minute -AmPm $Matches[4]
        }
        return @{ Limited = $true; ResetTime = $reset }
    }
    return $null
}

function Test-UsageLimited {
    # Sends a tiny probe prompt to see whether the usage limit is currently active.
    Write-Log "Probing usage-limit status (model: $($script:Config.ProbeModel))..."
    $result = Invoke-Claude -Arguments @('-p', 'Reply with exactly: OK', '--model', $script:Config.ProbeModel) -WorkingDirectory $script:ProbeDir
    $limit = Get-LimitInfo -Text $result.Output
    if ($limit) {
        if ($limit.ResetTime) {
            Write-Log "Usage limit ACTIVE. Resets at $($limit.ResetTime.ToString('yyyy-MM-dd h:mmtt'))."
        } else {
            Write-Log "Usage limit ACTIVE, but could not parse the reset time. Output was: $($result.Output.Trim())"
        }
        return $limit
    }
    if ($result.ExitCode -ne 0) {
        Write-Log "WARNING: probe exited with code $($result.ExitCode) but no limit message detected: $($result.Output.Trim())"
    } else {
        Write-Log 'No usage limit active.'
    }
    return $null
}

# ---------------------------------------------------------------------------
# Session discovery
# ---------------------------------------------------------------------------
function Get-SessionCwd {
    param([string]$JsonlPath)
    $lines = @(Get-Content $JsonlPath -Tail 50 -ErrorAction SilentlyContinue)
    for ($i = $lines.Count - 1; $i -ge 0; $i--) {
        try {
            $obj = $lines[$i] | ConvertFrom-Json
            if ($obj.cwd) { return [string]$obj.cwd }
        } catch { }
    }
    return $null
}

function Get-RecentSessions {
    param([datetime]$Since)
    $roots = @((Join-Path $env:USERPROFILE '.claude\projects'))
    if ($env:APPDATA) {
        # Some desktop-app builds keep sessions here instead of ~/.claude.
        $roots += (Join-Path $env:APPDATA 'Claude\claude-code-sessions')
    }
    $guidPattern = '^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'

    $files = @()
    foreach ($root in $roots) {
        if (Test-Path $root) {
            $files += Get-ChildItem -Path $root -Recurse -Filter '*.jsonl' -File -ErrorAction SilentlyContinue |
                Where-Object { $_.LastWriteTime -ge $Since -and $_.BaseName -match $guidPattern }
        }
    }

    # Newest session per project directory, so we resume one thread per project.
    $sessions = @()
    foreach ($group in ($files | Group-Object DirectoryName)) {
        $newest = $group.Group | Sort-Object LastWriteTime -Descending | Select-Object -First 1
        $cwd = Get-SessionCwd -JsonlPath $newest.FullName
        if (-not $cwd) { continue }
        if ($cwd -ieq $script:ProbeDir) { continue }            # our own probe sessions
        if (-not (Test-Path $cwd)) {
            Write-Log "Skipping session $($newest.BaseName): working directory '$cwd' no longer exists."
            continue
        }
        if ($script:Config.Projects.Count -gt 0) {
            $match = $script:Config.Projects | Where-Object { $cwd -ieq $_ }
            if (-not $match) { continue }
        }
        $sessions += [pscustomobject]@{
            Id        = $newest.BaseName
            Cwd       = $cwd
            LastWrite = $newest.LastWriteTime
        }
    }
    return @($sessions | Sort-Object LastWrite -Descending | Select-Object -First $script:Config.MaxSessionsPerRun)
}

# ---------------------------------------------------------------------------
# Pending state (survives reboots; the Scheduled Task picks it back up)
# ---------------------------------------------------------------------------
function Save-Pending {
    param($Sessions, $ResetTime)
    $state = @{
        DetectedAt = (Get-Date).ToString('o')
        ResetTime  = if ($ResetTime) { $ResetTime.ToString('o') } else { $null }
        Sessions   = @($Sessions | ForEach-Object { @{ Id = $_.Id; Cwd = $_.Cwd } })
    }
    $state | ConvertTo-Json -Depth 5 | Set-Content -Path $script:PendingFile
}

function Get-Pending {
    if (-not (Test-Path $script:PendingFile)) { return $null }
    try {
        $state = Get-Content $script:PendingFile -Raw | ConvertFrom-Json
        $reset = $null
        if ($state.ResetTime) { $reset = [datetime]::Parse($state.ResetTime) }
        return @{
            ResetTime = $reset
            Sessions  = @($state.Sessions | ForEach-Object { [pscustomobject]@{ Id = $_.Id; Cwd = $_.Cwd } })
        }
    } catch {
        Write-Log "WARNING: Could not read pending state, discarding it. ($($_.Exception.Message))"
        Remove-Item $script:PendingFile -Force -ErrorAction SilentlyContinue
        return $null
    }
}

function Clear-Pending {
    Remove-Item $script:PendingFile -Force -ErrorAction SilentlyContinue
}

# ---------------------------------------------------------------------------
# Waiting and resuming
# ---------------------------------------------------------------------------
function Wait-UntilReset {
    param([datetime]$ResetTime)
    $target = $ResetTime.AddMinutes($script:Config.ResetBufferMinutes)
    Write-Log "Waiting until $($target.ToString('yyyy-MM-dd h:mmtt')) to resume..."
    while ((Get-Date) -lt $target) {
        $remaining = $target - (Get-Date)
        if ($remaining.TotalMinutes -gt 10) {
            Write-Log ("  {0:hh\:mm\:ss} remaining..." -f $remaining)
            Start-Sleep -Seconds 600
        } else {
            Start-Sleep -Seconds ([Math]::Max(5, [int]$remaining.TotalSeconds))
        }
    }
}

function Resume-Session {
    # Returns 'ok', 'limited', or 'failed'.
    param($Session)
    Write-Log "Resuming session $($Session.Id) in '$($Session.Cwd)' [$($script:Config.Mode)]..."

    if ($script:Config.Mode -ieq 'interactive') {
        # cmd needs the whole command wrapped in an extra quote pair when both
        # the exe path and an argument are quoted.
        $inner = "`"$script:ClaudeCmd`" --resume $($Session.Id) `"$($script:Config.Prompt)`""
        Start-Process -FilePath 'cmd.exe' -ArgumentList "/k `"$inner`"" -WorkingDirectory $Session.Cwd
        Write-Log '  Opened an interactive terminal for this session.'
        return 'ok'
    }

    $resumeArgs = @('-p', $script:Config.Prompt, '--resume', $Session.Id, '--output-format', 'json')
    $resumeArgs += @($script:Config.ExtraArgs | ForEach-Object { "$_" })
    $result = Invoke-Claude -Arguments $resumeArgs -WorkingDirectory $Session.Cwd

    $limit = Get-LimitInfo -Text $result.Output
    if ($limit) {
        Write-Log '  Hit the usage limit again while resuming.'
        return 'limited'
    }
    if ($result.ExitCode -ne 0) {
        Write-Log "  FAILED (exit $($result.ExitCode)): $($result.Output.Trim())"
        return 'failed'
    }
    try {
        $json = $result.Output | ConvertFrom-Json
        $summary = "$($json.result)"
        if ($summary.Length -gt 300) { $summary = $summary.Substring(0, 300) + '...' }
        Write-Log "  Done (new session id: $($json.session_id)). Result: $summary"
    } catch {
        Write-Log '  Done.'
    }
    return 'ok'
}

function Resume-PendingSessions {
    # Resumes everything in $Sessions, re-waiting if the limit trips again.
    param($Sessions, $ResetTime)

    $remaining = @($Sessions)
    $attempts = 0
    while ($remaining.Count -gt 0 -and $attempts -lt 6) {
        $attempts++

        if ($ResetTime) {
            Save-Pending -Sessions $remaining -ResetTime $ResetTime
            Wait-UntilReset -ResetTime $ResetTime
        }

        # Confirm the window actually reset before burning the resume prompts.
        $limit = Test-UsageLimited
        if ($limit) {
            if ($limit.ResetTime) {
                $ResetTime = $limit.ResetTime
            } else {
                $ResetTime = (Get-Date).AddMinutes($script:Config.UnknownResetRetryMinutes)
                Write-Log "Reset time unknown; will re-check in $($script:Config.UnknownResetRetryMinutes) minutes."
            }
            continue
        }

        $stillPending = @()
        for ($i = 0; $i -lt $remaining.Count; $i++) {
            $status = Resume-Session -Session $remaining[$i]
            if ($status -eq 'limited') {
                # Keep this session and everything not yet attempted for the retry.
                $stillPending = @($remaining[$i..($remaining.Count - 1)])
                $probe = Test-UsageLimited
                if ($probe -and $probe.ResetTime) { $ResetTime = $probe.ResetTime }
                else { $ResetTime = (Get-Date).AddMinutes($script:Config.UnknownResetRetryMinutes) }
                break
            }
        }
        $remaining = $stillPending
    }

    if ($remaining.Count -gt 0) {
        Write-Log "Giving up after $attempts attempts; $($remaining.Count) session(s) left pending for the next run."
        Save-Pending -Sessions $remaining -ResetTime $ResetTime
        return $false
    }
    Clear-Pending
    Write-Log 'All sessions resumed.'
    return $true
}

# ---------------------------------------------------------------------------
# Main cycles
# ---------------------------------------------------------------------------
function Invoke-Cycle {
    # One detection/resume cycle. Returns $true if it did (or attempted) a resume.

    # 1. Anything left over from a previous run (e.g. interrupted by a reboot)?
    $pending = Get-Pending
    if ($pending -and $pending.Sessions.Count -gt 0) {
        Write-Log "Found pending state with $($pending.Sessions.Count) session(s) from a previous run."
        Resume-PendingSessions -Sessions $pending.Sessions -ResetTime $pending.ResetTime | Out-Null
        return $true
    }
    Clear-Pending

    # 2. Probe for an active limit.
    $limit = Test-UsageLimited
    if (-not $limit) { return $false }

    # 3. Snapshot the sessions that were active before the limit hit.
    $sessions = Get-RecentSessions -Since (Get-Date).AddHours(-$script:Config.LookbackHours)
    if ($sessions.Count -eq 0) {
        Write-Log "Limit is active, but no sessions were modified in the last $($script:Config.LookbackHours) hours. Nothing to resume."
        return $false
    }
    Write-Log "Will resume $($sessions.Count) session(s) after reset:"
    foreach ($s in $sessions) {
        Write-Log "  $($s.Id)  ($($s.Cwd), last active $($s.LastWrite.ToString('h:mmtt')))"
    }

    $reset = $limit.ResetTime
    if (-not $reset) { $reset = (Get-Date).AddMinutes($script:Config.UnknownResetRetryMinutes) }
    Resume-PendingSessions -Sessions $sessions -ResetTime $reset | Out-Null
    return $true
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------
$script:Config = Get-Config
$script:ClaudeCmd = Get-ClaudeCommand
Write-Log "--- Claude Auto-Resume starting (mode: $(if ($Watch) {'watch'} elseif ($Force) {'force'} else {'one-shot'}), resume style: $($script:Config.Mode)) ---"
Write-Log "Using claude at: $script:ClaudeCmd"

if ($Force) {
    $sessions = Get-RecentSessions -Since (Get-Date).AddHours(-$script:Config.LookbackHours)
    if ($sessions.Count -eq 0) {
        Write-Log "No sessions modified in the last $($script:Config.LookbackHours) hours."
        exit 0
    }
    foreach ($session in $sessions) { Resume-Session -Session $session | Out-Null }
    exit 0
}

if ($Watch) {
    while ($true) {
        try {
            Invoke-Cycle | Out-Null
        } catch {
            Write-Log "ERROR in cycle: $($_.Exception.Message)"
        }
        Start-Sleep -Seconds ($script:Config.CheckIntervalMinutes * 60)
    }
}

# One-shot mode
$acted = Invoke-Cycle
if (-not $acted) {
    Write-Log 'Nothing to do. (Use -Force to resume recent sessions immediately, or -Watch to keep monitoring.)'
}
