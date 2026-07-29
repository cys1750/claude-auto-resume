# Builds Constellate.exe on Windows. Requires Go (https://go.dev/dl/) and git.
#
#   powershell -ExecutionPolicy Bypass -File build.ps1
#
# The result is dist\Constellate.exe — a single self-contained executable.
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$repo = 'https://github.com/JCarterJohnson/constellate.git'
# Pinned so a build is reproducible. Keep in sync with fetch-web.sh.
$rev  = '31508ea5630b4404409b468fbab249126ab6c18f'

foreach ($tool in 'go', 'git') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool is not on PATH. Install it and re-run this script."
    }
}

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("constellate-" + [guid]::NewGuid().ToString('N'))
try {
    Write-Host "Fetching Constellate web app at $($rev.Substring(0,12))..."
    git init -q $work
    git -C $work remote add origin $repo
    git -C $work fetch -q --depth 1 origin $rev
    if ($LASTEXITCODE -ne 0) {
        Write-Warning 'Pinned revision unavailable by sha, fetching default branch instead.'
        git -C $work fetch -q --depth 1 origin HEAD
        if ($LASTEXITCODE -ne 0) { throw 'Could not fetch the web app.' }
    }
    git -C $work checkout -q FETCH_HEAD

    # Fixes we carry against upstream; see patches\*.patch for what and why.
    # A failure here means upstream moved — re-check the fix, do not skip it.
    foreach ($patch in Get-ChildItem -Path patches -Filter *.patch -ErrorAction SilentlyContinue | Sort-Object Name) {
        Write-Host "Applying $($patch.Name)..."
        git -C $work apply $patch.FullName
        if ($LASTEXITCODE -ne 0) { throw "Failed to apply $($patch.Name)." }
    }

    if (Test-Path web) { Remove-Item -Recurse -Force web }
    New-Item -ItemType Directory -Force -Path web\vendor | Out-Null
    Copy-Item "$work\index.html"             web\index.html
    Copy-Item "$work\vendor\three.min.js"    web\vendor\three.min.js
    Copy-Item "$work\LICENSE"                web\UPSTREAM-LICENSE
    (git -C $work rev-parse HEAD) | Set-Content -NoNewline web\UPSTREAM-REVISION

    Write-Host 'Building Constellate.exe...'
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    # -H=windowsgui: no console window when the exe is double-clicked.
    # -s -w: strip debug info, roughly halving the binary.
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags '-s -w -H=windowsgui' -o dist\Constellate.exe .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }

    # The exporter is a console tool, so it keeps its console and prints normally.
    Write-Host 'Building Export-CodeSessions.exe...'
    go build -trimpath -ldflags '-s -w' -o dist\Export-CodeSessions.exe .\cmd\codesessions
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }

    Write-Host ''
    Write-Host 'Built dist\Constellate.exe — double-click it to run Constellate.' -ForegroundColor Green
    Write-Host 'Built dist\Export-CodeSessions.exe — run it to put Claude Code sessions on the map.' -ForegroundColor Green
}
finally {
    if (Test-Path $work) { Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue }
}
