# Claude Auto-Resume (Windows)

Automatically resumes your Claude Code sessions when the 5-hour usage-limit
window resets, so work continues without you having to babysit the reset clock.

## How it works

1. **Detect** — the script periodically sends a tiny probe prompt
   (`claude -p "Reply with exactly: OK" --model haiku`). If the usage limit is
   active, Claude Code returns a limit message (e.g.
   `You've hit your session limit · resets 3:45pm`), which the script parses to
   get the reset time.
2. **Snapshot** — it finds the sessions you were working on when the limit hit
   by scanning the session transcripts under `%USERPROFILE%\.claude\projects\`
   (and the desktop app's session folder, if present) for files modified in the
   last few hours. It picks the newest session per project.
3. **Wait** — it sleeps until the reset time (plus a small buffer), then
   re-probes to confirm the window actually reset.
4. **Resume** — for each session it runs
   `claude -p "<continue prompt>" --resume <session-id>` from that session's
   original working directory, so the agent picks up exactly where it stopped.

Pending state survives reboots: if your PC restarts mid-wait, the scheduled
task picks the resume back up at next logon.

## Quick setup (no terminal needed)

1. Make sure the Claude Code command line works: open Command Prompt and type
   `claude --version`. If it prints a version, you're set. If not, open
   PowerShell and run `irm https://claude.ai/install.ps1 | iex`, then close and
   reopen the terminal and check again.
2. Double-click **`setup.bat`** in this folder. A window will confirm the
   background watcher is installed — it starts automatically every time you
   log in to Windows.
3. That's it. To remove it later, double-click **`uninstall.bat`**.

Other double-clickables:
- **`resume-now.bat`** — one-time run in a visible window: if the limit is
  active right now, it waits for the reset and resumes, then exits.

If Windows SmartScreen shows "Windows protected your PC" when running a .bat,
click "More info" then "Run anyway" (the files are plain scripts you can open
in Notepad to inspect).

## Setup (manual)

Requirements: Windows 10/11, PowerShell 5.1+ (built in), and the `claude` CLI
on your PATH. The Claude Code desktop app installs the CLI; verify with
`claude --version` in a terminal. If it isn't on PATH, set `ClaudePath` in
`config.json`.

```powershell
# From this folder, install the background watcher (runs hidden at every logon):
.\Install-AutoResumeTask.ps1

# Remove it later:
.\Install-AutoResumeTask.ps1 -Uninstall
```

Or run it manually instead of installing the task:

```powershell
# One-shot: if the limit is active right now, wait for reset and resume, then exit
.\Claude-AutoResume.ps1

# Keep watching in this terminal
.\Claude-AutoResume.ps1 -Watch

# Resume the most recent sessions immediately, no limit check
.\Claude-AutoResume.ps1 -Force

# Custom continue prompt for this run
.\Claude-AutoResume.ps1 -Prompt "Finish the failing tests, then stop."
```

Logs and state live in `%LOCALAPPDATA%\ClaudeAutoResume\auto-resume.log`.

## Configuration

Copy `config.example.json` to `config.json` next to the script and edit as
needed. All keys are optional:

| Key | Default | Meaning |
|---|---|---|
| `Prompt` | "Continue working on the previous task..." | What gets sent to each resumed session. |
| `LookbackHours` | `5` | Only sessions active within this window are resumed. |
| `MaxSessionsPerRun` | `3` | Cap on sessions resumed per reset (newest first, one per project). |
| `Mode` | `headless` | `headless` continues work unattended; `interactive` opens a terminal window per session so you can take over. |
| `CheckIntervalMinutes` | `15` | How often watch mode probes for the limit. |
| `ResetBufferMinutes` | `3` | Extra wait past the reported reset time. |
| `UnknownResetRetryMinutes` | `30` | Re-check interval when the reset time can't be parsed. |
| `ProbeModel` | `haiku` | Cheap model used for limit probes. |
| `ClaudePath` | (PATH) | Full path to `claude.exe` if it's not on PATH. |
| `ExtraArgs` | `[]` | Extra args for headless resumes, e.g. `["--permission-mode", "acceptEdits"]`. |
| `Projects` | `[]` | Restrict resuming to these project paths (empty = all). |

**Permissions tip:** in headless mode Claude can't show permission prompts —
tools it isn't allowed to use are simply denied, which can stall progress.
Either pre-approve the tools you trust in each project's
`.claude/settings.local.json`, or set
`"ExtraArgs": ["--permission-mode", "acceptEdits"]` (auto-accepts file edits).
Only go further (e.g. `--dangerously-skip-permissions`) if you fully trust the
work the sessions are doing, since they'll run unattended.

## Desktop app & Remote Control notes

- **Desktop app**: sessions are scanned both in `%USERPROFILE%\.claude\projects\`
  and `%APPDATA%\Claude\claude-code-sessions`. If a desktop-app session doesn't
  show up, resume happens via the CLI either way — open the project folder in a
  terminal once and run `claude --resume` to confirm the session is visible to
  the CLI.
- **Remote Control**: a headless resume keeps the transcript up to date, but
  doesn't expose a live remote-controllable session. If you want to grab the
  resumed session from your phone, set `"Mode": "interactive"` — each resume
  then opens a terminal window running `claude --resume <id>`, and you can run
  `/remote-control` in it (or pre-enable remote control in settings) to attach
  from claude.ai on another device.

## Caveats

- Resuming sends a real prompt, so each auto-resume consumes usage from the new
  window and the agent continues working unattended. Keep `MaxSessionsPerRun`
  low and the `Prompt` conservative (the default tells Claude to stop if the
  task is already done).
- The limit-message format isn't an official machine-readable API; the parser
  handles the known formats (`resets 3:45pm`, `resets Mon 12:00am`, and the
  `...|<unix-epoch>` form) and falls back to re-checking every 30 minutes if it
  can't parse a time.
- Each `--resume` in headless mode forks a new session ID; the tool always picks
  the newest transcript per project, so repeated resumes chain correctly.
