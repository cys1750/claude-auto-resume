# Constellate for Windows

[Constellate](https://github.com/JCarterJohnson/constellate) turns your LLM chat
history into an explorable 3D map. Upstream ships two apps: a zero-build web app
and a native macOS app. This directory builds the Windows equivalent — a single
`Constellate.exe` with the app inside it and nothing to install.

Everything still runs locally. The launcher serves the app to your own machine
over the loopback interface and makes no outbound connections.

## Get it running

Double-click `Constellate.exe`. It opens a Constellate window; close the window
and the launcher exits with it.

First run shows an empty map — click **Load demo universe** to explore ~150
synthetic conversations, or drag a ChatGPT/Claude export `.zip`, a Gemini Takeout
`MyActivity.json`, or markdown files onto the window to import your own.

Two things to expect the first time:

- **Windows SmartScreen** will warn about an unrecognized app, because the
  executable is unsigned. *More info → Run anyway*.
- It needs **Chrome or Edge** installed (Edge ships with Windows). If neither is
  found the launcher falls back to your default browser and prints the address to
  open; folder sync will not work there, since only Chromium browsers implement
  the File System Access API.

Your imported map is cached in browser storage under `%LOCALAPPDATA%\Constellate`
and survives restarts. "Erase all local data" in the app's Export menu clears it.

## Why an exe and not just index.html

The upstream web app is designed to be opened straight from disk, and it does
work that way. But a `file://` page is a second-class origin in every browser:
IndexedDB is unreliable there, so an imported map can vanish on reload, and
`showDirectoryPicker` — the whole basis of folder sync — is unavailable. Serving
the identical files from `http://127.0.0.1` makes it an ordinary secure origin, so
both features work exactly as they do in the macOS app.

The launcher opens the map in a Chromium **app window** (no tabs or address bar)
against a browser profile of its own under `%LOCALAPPDATA%\Constellate`. That
keeps Constellate's stored data separate from your everyday browsing, and it is
what lets the launcher shut down when you close the window.

The port is fixed at **47615** because the port is part of the origin, and the
origin is what your imported map is stored against. If something else already
holds it the launcher scans upward and logs the change — a map imported on one
port is not visible from another.

## Putting Claude Code sessions on the map

Your claude.ai export contains chat only. Claude Code sessions live on your own
disk, as one JSONL file per session under `%USERPROFILE%\.claude\projects` — the
same folders `Claude-AutoResume.ps1` scans.

Constellate cannot read those files even though it accepts `.jsonl`: its parser
expects one *conversation* per line with a `messages` array, while Claude Code
writes one *event* per line. Every line fails the check and you get an import
with nothing in it. `Export-CodeSessions.exe` rewrites them into the snapshot
schema Constellate does understand.

```
Export-CodeSessions.exe                    # write claude-code-sessions.json
```

Then drag that file onto the Constellate window. Sessions arrive under their own
`claude-code` provider, so you can toggle them on and off like any other source.
Titles come out as `project — first prompt` (or the `/compact` summary when a
session has one), which is what you see on the node labels.

Only prose is exported. Tool calls, tool output, thinking blocks and subagent
transcripts are dropped, because a session's file contents and command output
would otherwise dominate the topic model that decides where it sits on the map.
`-include-tools` adds a list of tool names per session, `-include-sidechains`
folds subagent work in.

Useful flags:

| Flag | What it does |
| --- | --- |
| `-list` | show the projects found, with session counts and dates; write nothing |
| `-exclude <text>` | skip projects whose path contains `<text>` (repeatable) |
| `-project <text>` | only projects whose path contains `<text>` (repeatable) |
| `-since 2026-01-01` | skip anything older |
| `-min-messages 2` | skip sessions shorter than this (default 2) |
| `-provider claude` | file them under an existing provider instead of `claude-code` |
| `-out <file>` | where to write |

Start with `-list` to see what you have, then exclude what you don't want on the
map:

```
Export-CodeSessions.exe -list
Export-CodeSessions.exe -exclude work-repo -exclude scratch -since 2026-01-01
```

## Removing conversations

The app itself is all-or-nothing: **Export ▾ → Erase all local data** wipes
everything, and there is no per-conversation or per-project delete. Provider
checkboxes and cluster filters only hide nodes; the data stays.

For code sessions, the filters above are the clean answer — leave a project out
of the export and it never reaches the map.

For anything already imported, including chats, prune a snapshot and re-import:

1. **Export ▾ → Snapshot** in Constellate, which saves `constellate-snapshot.json`.
2. `Export-CodeSessions.exe -prune constellate-snapshot.json -exclude "tax"`
   — the same `-project`, `-exclude` and `-since` filters apply, matched against
   the conversation title. It prints what it removed and writes
   `constellate-pruned.json`.
3. **Export ▾ → Erase all local data**, then drag the pruned file onto the window.

The erase step is not optional: importing merges rather than replaces, so
re-importing alone would leave the old conversations in place. Pruning copies
each kept conversation through untouched, so nothing else about your map changes.

## Viewing it on a phone or tablet

Two things stand in the way of using the map on Android or an iPad, and both are
handled here: the launcher only listens on loopback, and the app's camera was
wired for a mouse.

**Let other devices reach it.** `-lan` serves your network as well as this
machine and prints the address to type into the phone. Flags mean running it from
a terminal rather than double-clicking — Command Prompt or PowerShell both work,
from the folder holding the exe (PowerShell needs the `.\` prefix):

```powershell
.\Constellate.exe -lan -snapshot "$env:USERPROFILE\Downloads\constellate-snapshot.json"
```

```bat
Constellate.exe -lan -snapshot "%USERPROFILE%\Downloads\constellate-snapshot.json"
```

The launcher attaches to whichever console started it, so the addresses appear
there; they also go to `%LOCALAPPDATA%\Constellate\launcher.log`. Your shell gets
its prompt back straight away — that is normal for a windowed app, and the
launcher keeps running until you close the Constellate window.

**If you cannot run it from a terminal at all** — plenty of managed Windows
machines block that while still allowing a double-click — the same two settings
can be switched on with files placed next to `Constellate.exe`:

| File beside the exe | Same as |
| --- | --- |
| `enable-lan.txt` (any contents, even empty) | `-lan` |
| `constellate-snapshot.json` | `-snapshot <that file>` |

Create the first with right-click → *New* → *Text Document*, and put your
exported snapshot beside the exe. Double-click `Constellate.exe` as usual; it
reports which files it found. Delete `enable-lan.txt` to stop serving the network
— there is no exposure unless that file is deliberately there.

Explorer hides known extensions, so `enable-lan`, `enable-lan.txt` and
`enable-lan.txt.txt` are all accepted, and any `constellate-snapshot*.json` counts
— including the `constellate-snapshot (1).json` a second export leaves behind, of
which the newest wins.

The same trick covers any other flag without a terminal: right-click → *New* →
*Shortcut*, point it at the exe, then add the flags to *Target* in the shortcut's
properties. A shortcut is launched by Explorer, which is the path that works on a
machine where the shell refuses to start executables.

Read that flag as what it is: while the window is open, anything on the network
can read your map — no password, no encryption. It is off by default for that
reason, and the launcher still refuses requests that arrive under any name other
than this machine's own addresses, which is what stops a web page you happen to
have open from reaching in. Windows will ask whether to allow the app through the
firewall the first time; it needs the private-network box, not the public one.

**Get your map onto the device.** Browser storage is per-device, so a phone opens
to an empty map even though your PC has a full one. Export ▾ → *Snapshot* on the
PC, then point `-snapshot` at the file: the app loads it automatically when it
finds its own storage empty (patch `0003`). Without the flag nothing is served
and nothing changes.

**Gestures and layout.** Patch `0002` adds pinch-to-zoom and two-finger pan, which
the app otherwise has no equivalent of — zoom was wheel-only and panning needed a
right button. One finger still orbits; a pinch is not mistaken for a tap. Patches
`0004` and `0006` make the layout fit: the sidebar starts collapsed below 700px,
and the header stops pushing its own buttons off the screen.

What still does not work on a phone: folder sync (the File System Access API is
Chromium-desktop only), and dragging files onto the window to import them. Import
through the button instead.

## One file for the phone, with no server at all

`Make-Phone-Bundle.exe` writes the whole map into a single self-contained `.html`
— the app, Three.js and your snapshot inlined, nothing left to fetch. Move that
one file to a phone however you already move files, and open it in Chrome.

No `-lan`, no firewall rule, nothing exposed on any network, and it works offline
on cellular. It is also the answer for a remote-desktop or VDI session, where the
phone has no route to the machine the map lives on.

1. Export ▾ → *Snapshot* in Constellate.
2. Put `constellate-snapshot.json` next to `Make-Phone-Bundle.exe` and double-click
   it. With no arguments it also looks in `Downloads` and on the `Desktop`, newest
   first, and writes `constellate-phone.html` beside itself.
3. Copy that file to the phone and open it.

It is a frozen copy: no importing on the phone, and you regenerate it when you want
newer data. About 0.7 MB of app plus the size of your snapshot.

Flags, if a terminal is available: `-snapshot <file>` and `-out <file>`.

## Build it yourself

The build fetches the upstream web app at a pinned revision, applies the patches
in `patches/`, and compiles it into the executable. Upstream code is not vendored
into this repository.

**On Windows** — needs [Go](https://go.dev/dl/) and git on `PATH`:

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1
```

**On Linux or macOS** — cross-compiles, no Windows toolchain needed:

```sh
./build.sh              # dist/Constellate.exe and dist/Constellate-arm64.exe
./build.sh amd64        # just the Intel/AMD build most PCs need
```

Both produce a standalone executable in `dist/`. `go test ./...` covers the
launcher's request handling and port fallback.

Take `Constellate-arm64.exe` only if you have an ARM PC (Snapdragon-based
Surface and similar); `Constellate.exe` is the one for every Intel or AMD
machine.

## Patches carried against upstream

Each patch file explains itself in full; `patches/upstream-report.md` holds the
upstream-facing ones written up as issues, ready to file against the project.

| Patch | What it fixes |
| --- | --- |
| `0001-paint-nodes-after-rebuild` | blank 3D viewport on first load: `rebuildAll()` never calls `draw()`, so every node had size 0, alpha 0 and no visibility flag, and the simulation skipped all of them. The sidebar and timeline populated normally, so it read as a GPU fault rather than a missing repaint. |
| `0002-touch-gestures` | pinch-to-zoom and two-finger pan. Zoom was wheel-only and panning needed a right button, so a touchscreen could rotate the map and nothing else. |
| `0003-load-offered-snapshot` | lets the page load a snapshot inlined into it or served alongside it, when local storage is empty. This is what carries a map to another device; nothing is offered by default. |
| `0004-collapse-sidebar-on-narrow-screens` | the sidebar is a fixed 248px with no media queries, leaving 164px of map on a phone. Starts collapsed below 700px. |
| `0005-remeasure-canvas-when-timeline-appears` | clicks and taps missed the node they were aimed at. Revealing the timeline shortens the canvas and nothing re-measured, so `CW`/`CH` and the camera aspect described a canvas taller than the real one — 770 against 708 at 1280x820, putting the ray ~79 world units off target and stretching the render. |
| `0006-header-fits-a-phone` | at 412px the header tagline wrapped onto three lines and pushed Export and Help off the right edge. |

`0001`, `0002`, `0005` and `0006` are upstream bugs rather than Windows-specific
ones, and `0004` is an upstream shortcoming. `0003` is the only one that exists for
this launcher's sake, serving `-snapshot` and the phone bundle.

## Layout

| Path | What it is |
| --- | --- |
| `main.go` | the launcher: embeds the app, serves loopback, opens the window |
| `dialog_windows.go` | fatal-error message box (for when there is no console) |
| `console_windows.go` | attaches the launching terminal's console so flags can report |
| `main_test.go` | tests for host checking, serving, and port fallback |
| `cmd/codesessions/` | `Export-CodeSessions.exe`: transcripts → snapshot, and `-prune` |
| `cmd/bundle/` | `Make-Phone-Bundle.exe`: the map as one self-contained .html |
| `site/` | the web app, staged by `fetch-web.sh`, compiled into both binaries |
| `fetch-web.sh` | fetches + patches the upstream web app into `web/` |
| `build.sh` / `build.ps1` | build the executable |
| `patches/` | the fixes carried against upstream, each explaining itself |
| `patches/upstream-report.md` | those fixes written up as issues to file upstream |

`site/web/` and `dist/` are build products and are not committed.

## Credits

Constellate is by [JCarterJohnson](https://github.com/JCarterJohnson/constellate),
MIT licensed; the upstream licence is copied into the build as
`site/web/UPSTREAM-LICENSE`. This directory only packages it for Windows.
