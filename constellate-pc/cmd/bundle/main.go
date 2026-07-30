// Make-Phone-Bundle writes the whole map into one self-contained .html file: the
// app, Three.js and your snapshot, with nothing left to fetch.
//
// Copy that file to a phone or tablet however you already move files and open it
// in Chrome. No server, no network exposure, no firewall rule, and it works
// offline. The trade-off is that it is a frozen copy — regenerate it when you
// want newer data.
//
// Double-clicking is enough: with no arguments it looks for a snapshot beside
// itself and then in your Downloads folder, and writes the bundle alongside the
// executable.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"constellate-pc/site"
)

const (
	threeTag = `<script src="./vendor/three.min.js"></script>`
	// Matches what the app exports (constellate-snapshot.json), what -prune writes
	// and what curate.html saves (constellate-pruned.json), and the "(1)" copies a
	// second download leaves behind. Newest wins.
	snapshotGlob = "constellate-*.json"
	defaultOut   = "constellate-phone.html"
)

func main() {
	out := flag.String("out", "", "file to write (default: "+defaultOut+" beside this program)")
	in := flag.String("snapshot", "", "snapshot to embed (default: the newest one found)")
	flag.Parse()

	// Keep a double-clicked window up long enough to read.
	defer waitForEnter()

	dir := exeDir()

	snapshotPath := *in
	if snapshotPath == "" {
		var searched []string
		snapshotPath, searched = findSnapshot(dir)
		if snapshotPath == "" {
			fail("No snapshot found. Looked in:\n  %s\n\n"+
				"In Constellate: Export ▾ → Snapshot, then put constellate-snapshot.json\n"+
				"next to this program (or pass -snapshot <file>) and run it again.",
				strings.Join(searched, "\n  "))
		}
	}

	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		fail("Cannot read %s: %v", snapshotPath, err)
	}
	count, err := countConversations(data)
	if err != nil {
		fail("%s does not look like a Constellate snapshot: %v", filepath.Base(snapshotPath), err)
	}
	fmt.Printf("Read %d conversations from %s\n", count, snapshotPath)

	html, err := buildBundle(data)
	if err != nil {
		fail("%v", err)
	}

	target := *out
	if target == "" {
		target = filepath.Join(dir, defaultOut)
	}
	if err := os.WriteFile(target, html, 0o600); err != nil {
		fail("Cannot write %s: %v", target, err)
	}
	abs, _ := filepath.Abs(target)
	fmt.Printf("\nWrote %s (%.1f MB)\n", abs, float64(len(html))/(1<<20))
	fmt.Println("Copy that one file to your phone and open it in Chrome — it needs nothing else.")
}

// buildBundle inlines the vendored Three.js and the snapshot into the app's HTML,
// producing a file with no external references at all.
func buildBundle(snapshot []byte) ([]byte, error) {
	app, err := site.FS.ReadFile(site.AppFile)
	if err != nil {
		return nil, fmt.Errorf("this build has no web app embedded (run fetch-web.sh, then rebuild): %w", err)
	}
	three, err := site.FS.ReadFile(site.ThreeFile)
	if err != nil {
		return nil, fmt.Errorf("this build has no vendored Three.js embedded: %w", err)
	}
	if !bytes.Contains(app, []byte(threeTag)) {
		return nil, fmt.Errorf("the app no longer loads Three.js the expected way; bundling would silently\n" +
			"produce a file that cannot render. Update threeTag in cmd/bundle to match index.html")
	}

	var b bytes.Buffer
	b.Grow(len(app) + len(three) + len(snapshot) + 256)
	// Three.js in place of its script tag, so it still runs before the app.
	b.Write([]byte("<script>"))
	b.Write(escapeForScript(three))
	b.Write([]byte("</script>\n"))
	// The snapshot ahead of the app's own script, which reads it during boot.
	b.Write([]byte(`<script type="application/json" id="constellate-snapshot">`))
	b.Write(escapeForScript(snapshot))
	b.Write([]byte("</script>"))

	bundled := bytes.Replace(app, []byte(threeTag), b.Bytes(), 1)
	if bytes.Contains(bundled, []byte("./vendor/")) {
		return nil, fmt.Errorf("the bundle still references ./vendor/, so it would not work standalone")
	}
	return bundled, nil
}

// escapeForScript neutralises the only sequences that can end the surrounding
// script element early. Neither appears in the vendored Three.js, and in JSON a
// literal < can only occur inside a string, where the escape is equivalent.
func escapeForScript(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("</script"), []byte(`<\/script`))
	return bytes.ReplaceAll(b, []byte("<!--"), []byte(`<\!--`))
}

// countConversations checks the file really is a snapshot before it is embedded,
// so a mistake surfaces here rather than as an empty map on the phone.
func countConversations(data []byte) (int, error) {
	var snap struct {
		Conversations []json.RawMessage `json:"conversations"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return 0, err
	}
	if len(snap.Conversations) == 0 {
		return 0, fmt.Errorf("it contains no conversations")
	}
	return len(snap.Conversations), nil
}

// findSnapshot looks where an exported snapshot actually ends up: next to this
// program, or still in the Downloads folder the browser saved it to. Returns the
// newest match and the places searched.
func findSnapshot(dir string) (string, []string) {
	var searched []string
	if dir != "" {
		searched = append(searched, dir)
	}
	if home, err := os.UserHomeDir(); err == nil {
		searched = append(searched, filepath.Join(home, "Downloads"), filepath.Join(home, "Desktop"))
	}
	for _, place := range searched {
		if found := newestMatch(place); found != "" {
			return found, searched
		}
	}
	return "", searched
}

func newestMatch(dir string) string {
	matches, err := filepath.Glob(filepath.Join(dir, snapshotGlob))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		a, errA := os.Stat(matches[i])
		b, errB := os.Stat(matches[j])
		if errA != nil || errB != nil {
			return false
		}
		return a.ModTime().After(b.ModTime())
	})
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && !fi.IsDir() {
			return m
		}
	}
	return ""
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\n"+format+"\n", args...)
	waitForEnter()
	os.Exit(1)
}

// waitForEnter holds a double-clicked window open. Any argument means a real
// console, where pausing would only be in the way.
func waitForEnter() {
	if runtime.GOOS != "windows" || len(os.Args) > 1 {
		return
	}
	fmt.Print("\nPress Enter to close…")
	fmt.Fscanln(os.Stdin)
}
