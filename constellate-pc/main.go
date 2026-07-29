// Constellate for Windows — a single-executable launcher for the Constellate
// web app (https://github.com/JCarterJohnson/constellate).
//
// The upstream project ships a zero-build web app and a native macOS app. This
// launcher gives Windows the equivalent of the native experience: the web app
// is embedded in the executable, served from a loopback origin, and opened in a
// Chromium app window with its own profile.
//
// Serving over http://127.0.0.1 rather than opening index.html from file://
// matters for two features: IndexedDB (so the map survives a restart) and the
// File System Access API used by folder sync, neither of which browsers grant
// to file:// pages.
package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// The web app is fetched by the build script (see fetch-web.sh / build.ps1) and
// compiled in, so the shipped executable needs nothing alongside it.
//
//go:embed all:web
var embeddedWeb embed.FS

// The loopback port is part of the browser origin, and the origin is what
// IndexedDB keys its storage on. It has to stay stable across launches or the
// imported map disappears, so we always prefer the same port and only scan
// upward when something else already holds it.
const (
	defaultPort = 47615
	portScanLen = 12
)

func main() {
	port := flag.Int("port", defaultPort, "loopback port to serve on")
	noBrowser := flag.Bool("no-browser", false, "serve only; print the URL instead of opening a window")
	browserPath := flag.String("browser", "", "path to a Chromium browser to use instead of the autodetected one")
	flag.Parse()

	logFile := startLogging()
	if logFile != nil {
		defer logFile.Close()
	}

	site, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		fatal("The embedded copy of the web app is unreadable: %v", err)
	}
	if _, err := fs.Stat(site, "index.html"); err != nil {
		fatal("This executable was built without the web app embedded.\n\n" +
			"Run fetch-web.sh (or build.ps1, which does it for you) to download\n" +
			"the Constellate web app into constellate-pc/web, then rebuild.")
	}

	listener, actualPort, err := listen(*port)
	if err != nil {
		fatal("Could not open a local port to serve Constellate on: %v", err)
	}
	if actualPort != *port {
		log.Printf("port %d was busy; using %d instead (data saved under the other port stays there)", *port, actualPort)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/", actualPort)
	server := &http.Server{Handler: handler(site)}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
		}
	}()
	log.Printf("serving Constellate at %s", url)

	if *noBrowser {
		fmt.Printf("Constellate is running at %s\nPress Ctrl+C to stop.\n", url)
		waitForInterrupt()
		return
	}

	browser := findBrowser(*browserPath)
	if browser == "" {
		log.Print("no Chromium browser found; falling back to the default browser")
		if err := openInDefaultBrowser(url); err != nil {
			fatal("Constellate is running at %s but no browser could be opened: %v\n\n"+
				"Paste that address into Chrome or Edge to use it.", url, err)
		}
		fmt.Printf("Constellate is running at %s\n"+
			"Leave this window open while you use it. Press Ctrl+C to stop.\n", url)
		waitForInterrupt()
		return
	}

	log.Printf("launching %s", browser)
	cmd, err := startAppWindow(browser, url)
	if err != nil {
		fatal("Could not start %s: %v", filepath.Base(browser), err)
	}

	// The dedicated profile directory keeps this from handing the window off to
	// an already-running browser, so the child process really does live as long
	// as the window and we can quit with it.
	if err := cmd.Wait(); err != nil {
		log.Printf("browser exited: %v", err)
	}
	log.Print("window closed; shutting down")
}

// handler serves the embedded site. Everything is local and single-user, so the
// only header work needed is keeping the browser from caching a stale app after
// an upgrade.
func handler(site fs.FS) http.Handler {
	files := http.FileServer(http.FS(site))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject requests aimed at us under any other name — DNS rebinding is
		// the realistic way a web page could otherwise reach this server.
		if !isLoopbackHost(r.Host) {
			http.Error(w, "unexpected host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether a request's Host header names this machine.
// A Host with no port is valid HTTP, so the missing-port case has to be handled
// rather than treated as unparseable and waved through.
func isLoopbackHost(hostHeader string) bool {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// listen binds the preferred port, scanning upward if it is taken.
func listen(preferred int) (net.Listener, int, error) {
	var lastErr error
	for port := preferred; port < preferred+portScanLen; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
		lastErr = err
	}
	return nil, 0, lastErr
}

// findBrowser locates Chrome or Edge. Both are Chromium, which is what folder
// sync needs; Chrome is tried first because its File System Access
// implementation is the reference one.
func findBrowser(override string) string {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
		log.Printf("browser override %q not found; autodetecting", override)
	}
	for _, candidate := range browserCandidates() {
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
			continue
		}
		if resolved, err := exec.LookPath(candidate); err == nil {
			return resolved
		}
	}
	return ""
}

func browserCandidates() []string {
	if runtime.GOOS != "windows" {
		// Only used when developing the launcher on a non-Windows machine.
		return []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
	}
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	var out []string
	for _, suffix := range []string{
		`Google\Chrome\Application\chrome.exe`,
		`Microsoft\Edge\Application\msedge.exe`,
	} {
		for _, root := range roots {
			out = append(out, filepath.Join(root, suffix))
		}
	}
	return out
}

// startAppWindow opens the URL as a chromeless app window against a profile of
// our own, so Constellate gets a window that behaves like an application and
// its IndexedDB data is not entangled with the user's everyday browsing.
func startAppWindow(browser, url string) (*exec.Cmd, error) {
	profile := filepath.Join(dataDir(), "browser-profile")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, fmt.Errorf("creating browser profile directory: %w", err)
	}
	cmd := exec.Command(browser,
		"--app="+url,
		"--user-data-dir="+profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1600,1000",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func openInDefaultBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// dataDir is where the browser profile and the launcher log live: per-user,
// outside the executable's directory, so the exe itself stays portable.
func dataDir() string {
	base := os.Getenv("LocalAppData")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "Constellate")
}

// startLogging tees diagnostics to a file, because the Windows build is linked
// as a GUI application and has no console to print to.
func startLogging() *os.File {
	dir := dataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "launcher.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > 256*1024 {
		f.Truncate(0)
		f.Seek(0, io.SeekStart)
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	log.SetFlags(log.LstdFlags)
	log.Printf("--- Constellate launcher starting (%s/%s) ---", runtime.GOOS, runtime.GOARCH)
	return f
}

// fatal reports an unrecoverable problem where the user will actually see it:
// a dialog box on Windows, the terminal elsewhere.
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Print("fatal: " + msg)
	showFatalDialog(msg)
	os.Exit(1)
}

func waitForInterrupt() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	// Give the browser a moment to finish flushing IndexedDB writes.
	time.Sleep(200 * time.Millisecond)
}
