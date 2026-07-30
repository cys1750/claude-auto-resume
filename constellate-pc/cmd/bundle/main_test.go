package main

import (
	"bytes"
	"strings"
	"testing"
)

const snapshotJSON = `{"app":"constellate","conversations":[` +
	`{"id":"claude:a","provider":"claude","title":"Sourdough starter","created":1758401987221,` +
	`"messages":[{"role":"user","text":"dense crumb","ts":1758401987221}]}]}`

func TestBundleIsSelfContained(t *testing.T) {
	html, err := buildBundle([]byte(snapshotJSON))
	if err != nil {
		t.Fatalf("buildBundle: %v", err)
	}

	// Nothing may be left to fetch — the file has to work from file:// on a phone.
	for _, forbidden := range []string{`src="./`, `href="./`, "./vendor/"} {
		if bytes.Contains(html, []byte(forbidden)) {
			t.Errorf("bundle still references %q", forbidden)
		}
	}
	// Three.js inlined where its tag was, and the snapshot where the app reads it.
	if !bytes.Contains(html, []byte("THREE")) {
		t.Error("Three.js does not appear to be inlined")
	}
	if !bytes.Contains(html, []byte(`id="constellate-snapshot"`)) {
		t.Error("the snapshot element is missing")
	}
	if !bytes.Contains(html, []byte("Sourdough starter")) {
		t.Error("the snapshot's contents are missing")
	}
	// The app's own script must still be there, and after Three.js.
	three := bytes.Index(html, []byte("THREE"))
	boot := bytes.Index(html, []byte("async function boot()"))
	if boot < 0 {
		t.Fatal("the app's script is missing from the bundle")
	}
	if three > boot {
		t.Error("Three.js is inlined after the app that needs it")
	}
}

// A conversation title containing a closing script tag would otherwise end the
// element early and leave the rest of the map as visible page text.
func TestBundleNeutralisesScriptTerminators(t *testing.T) {
	hostile := `{"app":"constellate","conversations":[{"id":"x:1","title":"break </script><b>out</b>",` +
		`"messages":[{"role":"user","text":"and <!-- this"}]}]}`
	html, err := buildBundle([]byte(hostile))
	if err != nil {
		t.Fatalf("buildBundle: %v", err)
	}

	const open = `<script type="application/json" id="constellate-snapshot">`
	start := bytes.Index(html, []byte(open))
	if start < 0 {
		t.Fatal("snapshot element missing")
	}
	body := html[start+len(open):]
	end := bytes.Index(body, []byte("</script>"))
	if end < 0 {
		t.Fatal("snapshot element is not closed")
	}
	embedded := string(body[:end])

	if strings.Contains(embedded, "</script") {
		t.Error("a raw closing script tag survived inside the embedded snapshot")
	}
	if !strings.Contains(embedded, `<\/script`) {
		t.Error("the closing tag was not escaped")
	}
	if !strings.Contains(embedded, `<\!--`) {
		t.Error("a comment opener was not escaped")
	}
	// JSON.parse must still see the original text: \/ is just / in a JSON string.
	if strings.Contains(embedded, "break </script>") {
		t.Error("expected the escaped form, not the literal")
	}
}

func TestCountConversationsRejectsWhatIsNotASnapshot(t *testing.T) {
	if n, err := countConversations([]byte(snapshotJSON)); err != nil || n != 1 {
		t.Errorf("valid snapshot = (%d, %v), want (1, nil)", n, err)
	}
	for name, body := range map[string]string{
		"not json":         `<html>`,
		"no conversations": `{"app":"constellate"}`,
		"empty list":       `{"app":"constellate","conversations":[]}`,
	} {
		if _, err := countConversations([]byte(body)); err == nil {
			t.Errorf("%s: accepted, want an error before it reaches the phone", name)
		}
	}
}
