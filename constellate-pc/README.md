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

`patches/0001-paint-nodes-after-rebuild.patch` fixes a blank 3D viewport on
first load. `rebuildAll()` never calls `draw()`, and `draw()` is what fills in
each node's colour, size and visibility — so after loading the demo or importing
an export, every node had size 0 and alpha 0 and the force simulation skipped
all of them. The sidebar, clusters and timeline populated normally, which made it
look like a GPU problem rather than a missing repaint. Adding the one `draw()`
call takes a freshly loaded demo from 0 visible nodes to all 150.

This is an upstream bug, not a Windows-specific one, and it is worth reporting to
the project.

## Layout

| Path | What it is |
| --- | --- |
| `main.go` | the launcher: embeds the app, serves loopback, opens the window |
| `dialog_windows.go` | fatal-error message box (the exe has no console) |
| `main_test.go` | tests for host checking, serving, and port fallback |
| `fetch-web.sh` | fetches + patches the upstream web app into `web/` |
| `build.sh` / `build.ps1` | build the executable |
| `patches/` | the fixes carried against upstream, each explaining itself |

`web/` and `dist/` are build products and are not committed.

## Credits

Constellate is by [JCarterJohnson](https://github.com/JCarterJohnson/constellate),
MIT licensed; the upstream licence is copied into the build as
`web/UPSTREAM-LICENSE`. This directory only packages it for Windows.
