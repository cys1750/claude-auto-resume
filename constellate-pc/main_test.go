package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"constellate-pc/site"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:47615":   true,
		"127.0.0.1":         true, // a Host header may legally omit the port
		"127.0.0.53:47615":  true,
		"localhost:47615":   true,
		"LocalHost":         true,
		"[::1]:47615":       true,
		"::1":               true,
		"evil.example":      false,
		"evil.example:80":   false,
		"192.168.1.7:47615": false,
		"":                  false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestHandlerServesAppAndRejectsForeignHosts(t *testing.T) {
	site := fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html>hello")},
		"vendor/three.min.js": {Data: []byte("// three")},
	}
	h := handler(site, "", hostSet{})

	// http.FileServer redirects /index.html to / by design; the app only ever
	// requests / and its one vendored script.
	for _, path := range []string{"/", "/vendor/three.min.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615"+path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s: missing no-store, got %q", path, rec.Header().Get("Cache-Control"))
		}
	}

	// A page on another origin resolving a name to 127.0.0.1 must not be served.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615/", nil)
	req.Host = "attacker.example"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host = %d, want 403", rec.Code)
	}
}

func TestListenFallsBackWhenPortIsBusy(t *testing.T) {
	first, port, err := listen(defaultPort, false)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer first.Close()

	second, next, err := listen(port, false)
	if err != nil {
		t.Fatalf("second listen: %v", err)
	}
	defer second.Close()

	if next == port {
		t.Fatalf("second listen reused busy port %d", port)
	}
	if next != port+1 {
		t.Errorf("second listen = %d, want %d (the next port up)", next, port+1)
	}
}

func TestEmbeddedAppIsPresent(t *testing.T) {
	// Guards against shipping a build whose fetch step never ran.
	for _, name := range []string{"web/index.html", "web/vendor/three.min.js"} {
		if _, err := site.FS.ReadFile(name); err != nil {
			t.Errorf("%s not embedded: %v", name, err)
		}
	}
}

func TestSnapshotIsOnlyServedWhenOffered(t *testing.T) {
	site := fstest.MapFS{"index.html": {Data: []byte("app")}}

	// Without -snapshot the app's probe must 404 rather than reach the site's
	// file server, which would answer with the app itself.
	rec := httptest.NewRecorder()
	handler(site, "", hostSet{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615/"+snapshotName, nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("un-offered snapshot = %d, want 404", rec.Code)
	}

	path := filepath.Join(t.TempDir(), "snap.json")
	body := `{"app":"constellate","conversations":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	handler(site, path, hostSet{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615/"+snapshotName, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != body {
		t.Errorf("offered snapshot = %d %q", rec.Code, rec.Body.String())
	}
}

func TestLanModeAnswersOnlyToItsOwnAddresses(t *testing.T) {
	site := fstest.MapFS{"index.html": {Data: []byte("app")}}
	hosts := hostSet{lan: true, local: map[string]bool{"192.168.1.7": true, "my-pc": true}}
	h := handler(site, "", hosts)

	serve := func(host string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615/", nil)
		req.Host = host
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for host, want := range map[string]int{
		"192.168.1.7:47615": http.StatusOK,
		"my-pc:47615":       http.StatusOK,
		"127.0.0.1:47615":   http.StatusOK,
		// A page that rebinds a name it owns to our address still gets nothing.
		"attacker.example:47615": http.StatusForbidden,
		"192.168.1.99:47615":     http.StatusForbidden,
	} {
		if got := serve(host); got != want {
			t.Errorf("lan Host %q = %d, want %d", host, got, want)
		}
	}

	// Off by default: the same LAN address is refused without -lan.
	if got := handlerCode(t, handler(site, "", allowedHosts(false)), "192.168.1.7:47615"); got != http.StatusForbidden {
		t.Errorf("without -lan, LAN Host = %d, want 403", got)
	}
}

func handlerCode(t *testing.T, h http.Handler, host string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47615/", nil)
	req.Host = host
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestSidecarFilesStandInForFlags(t *testing.T) {
	dir := t.TempDir()

	// An empty folder changes nothing: no exposure without a deliberate file.
	lan, snapshot := false, ""
	if notes := resolveSidecars(dir, &lan, &snapshot); len(notes) != 0 || lan || snapshot != "" {
		t.Fatalf("empty folder enabled something: lan=%v snapshot=%q notes=%v", lan, snapshot, notes)
	}

	for _, name := range []string{lanSidecar, snapshotSidecar} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	notes := resolveSidecars(dir, &lan, &snapshot)
	if !lan {
		t.Errorf("%s did not enable lan mode", lanSidecar)
	}
	if snapshot != filepath.Join(dir, snapshotSidecar) {
		t.Errorf("snapshot = %q, want the file beside the exe", snapshot)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want one per setting so the user can see why", notes)
	}

	// An explicit flag wins, and stays reported as-is.
	lan, snapshot = true, "C:/elsewhere/snap.json"
	if notes := resolveSidecars(dir, &lan, &snapshot); len(notes) != 0 {
		t.Errorf("sidecars overrode explicit flags: %v", notes)
	}
	if snapshot != "C:/elsewhere/snap.json" {
		t.Errorf("snapshot flag was overwritten with %q", snapshot)
	}
}

// Explorer hides known extensions, so the file a user believes they created is
// often not the name they typed.
func TestSidecarToleratesExplorerFilenames(t *testing.T) {
	for _, name := range []string{"enable-lan.txt", "enable-lan", "enable-lan.txt.txt"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		lan, snapshot := false, ""
		resolveSidecars(dir, &lan, &snapshot)
		if !lan {
			t.Errorf("%q did not enable lan mode", name)
		}
	}

	// A re-downloaded snapshot keeps its "(1)" and is newer, so it should win.
	dir := t.TempDir()
	older := filepath.Join(dir, "constellate-snapshot.json")
	newer := filepath.Join(dir, "constellate-snapshot (1).json")
	for _, f := range []string{older, newer} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatal(err)
	}
	lan, snapshot := false, ""
	resolveSidecars(dir, &lan, &snapshot)
	if snapshot != newer {
		t.Errorf("snapshot = %q, want the newer %q", snapshot, newer)
	}
	if lan {
		t.Error("a snapshot file alone must not expose anything to the network")
	}
}
