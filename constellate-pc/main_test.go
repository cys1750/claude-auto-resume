package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
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
	h := handler(site)

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
	first, port, err := listen(defaultPort)
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer first.Close()

	second, next, err := listen(port)
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
		if _, err := embeddedWeb.ReadFile(name); err != nil {
			t.Errorf("%s not embedded: %v", name, err)
		}
	}
}
