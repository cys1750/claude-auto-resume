package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A transcript with the shapes a real session contains: plain-string prompts,
// content arrays mixing text with thinking and tool traffic, and the event types
// that are not conversation at all.
const transcript = `
{"type":"queue-operation","operation":"enqueue","sessionId":"s1","content":"queued copy of the prompt"}
{"type":"user","sessionId":"s1","timestamp":"2026-07-02T10:00:00.000Z","cwd":"/home/me/orbit-weaver","gitBranch":"main","message":{"role":"user","content":"How do I make an orbital puzzle feel fair?<system-reminder>ignore me</system-reminder>"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-07-02T10:00:09.000Z","cwd":"/home/me/orbit-weaver","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"thinking","thinking":"secret reasoning"},{"type":"text","text":"Telegraph the trajectory."},{"type":"tool_use","name":"Read","input":{"file":"a.go"}}]}}
{"type":"user","sessionId":"s1","timestamp":"2026-07-02T10:00:12.000Z","cwd":"/home/me/orbit-weaver","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"600 lines of file contents"}]}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-07-02T10:00:30.000Z","cwd":"/home/me/orbit-weaver","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Landed the fix."}]}}
{"type":"user","sessionId":"s1","timestamp":"2026-07-02T10:01:00.000Z","isMeta":true,"message":{"role":"user","content":"[Image: original 100x100]"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-07-02T10:02:00.000Z","isSidechain":true,"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"subagent chatter"}]}}
{"type":"attachment","sessionId":"s1","attachment":{"type":"deferred_tools_delta"}}
{"type":"system","sessionId":"s1","subtype":"stop_hook_summary","hookCount":1}
{"type":"user","sessionId":"s2","timestamp":"2026-07-03T09:00:00.000Z","cwd":"/home/me/tax-scripts","message":{"role":"user","content":"Only one message here"}}
{"broken json
`

// The same session continued in a second file, which is what resuming produces.
const transcriptResumed = `
{"type":"summary","sessionId":"s1","summary":"Orbit Weaver difficulty curve"}
{"type":"user","sessionId":"s1","timestamp":"2026-07-02T11:00:00.000Z","cwd":"/home/me/orbit-weaver","message":{"role":"user","content":"Now tune the difficulty curve"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-07-02T11:00:20.000Z","cwd":"/home/me/orbit-weaver","message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"Start with three gravity wells."}]}}
`

func writeTranscripts(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	proj := filepath.Join(dir, "-home-me-orbit-weaver")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"s1.jsonl":         transcript,
		"s1-resumed.jsonl": transcriptResumed,
	} {
		if err := os.WriteFile(filepath.Join(proj, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func collect(t *testing.T, dir string, includeSide, includeTools bool, minMsgs int) map[string]conv {
	t.Helper()
	sessions := map[string]*session{}
	for _, f := range findTranscripts([]string{dir}) {
		if err := readTranscript(f, sessions, includeSide, includeTools); err != nil {
			t.Fatalf("readTranscript(%s): %v", f, err)
		}
	}
	out := map[string]conv{}
	for id, s := range sessions {
		if c, ok := s.toConv("claude-code", minMsgs); ok {
			out[id] = c
		}
	}
	return out
}

func TestSessionBecomesConversation(t *testing.T) {
	convs := collect(t, writeTranscripts(t), false, false, 2)

	c, ok := convs["s1"]
	if !ok {
		t.Fatalf("session s1 missing; got %v", keys(convs))
	}
	if c.ID != "claude-code:s1" {
		t.Errorf("id = %q, want claude-code:s1 so re-imports dedupe", c.ID)
	}
	if c.Model != "claude-opus-5" {
		t.Errorf("model = %q", c.Model)
	}
	// A /compact summary makes a better title than the first prompt.
	if want := "orbit-weaver — Orbit Weaver difficulty curve"; c.Title != want {
		t.Errorf("title = %q, want %q", c.Title, want)
	}
	// Events from both files, in timestamp order at the edges.
	if c.Created != "2026-07-02T10:00:00.000Z" || c.Updated != "2026-07-02T11:00:20.000Z" {
		t.Errorf("span = %s .. %s, want the resumed file folded in", c.Created, c.Updated)
	}

	var joined string
	for _, m := range c.Messages {
		joined += m.Role + ": " + m.Text + "\n"
	}
	for _, want := range []string{"orbital puzzle feel fair", "Telegraph the trajectory", "tune the difficulty curve"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	// Everything that would pollute the topic model must be gone.
	for _, unwanted := range []string{"ignore me", "secret reasoning", "600 lines of file contents",
		"[Image:", "subagent chatter", "queued copy", "deferred_tools_delta"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("leaked %q into:\n%s", unwanted, joined)
		}
	}

	// One-message sessions are noise on a map.
	if _, ok := convs["s2"]; ok {
		t.Error("s2 has one message and should have been skipped")
	}
}

func TestOptionalContentIsOptIn(t *testing.T) {
	dir := writeTranscripts(t)

	withTools := collect(t, dir, false, true, 2)["s1"]
	if !strings.Contains(textOf(withTools), "[tools: Read]") {
		t.Errorf("-include-tools did not record the tool name: %s", textOf(withTools))
	}

	withSide := collect(t, dir, true, false, 2)["s1"]
	if !strings.Contains(textOf(withSide), "subagent chatter") {
		t.Errorf("-include-sidechains did not fold in the subagent transcript")
	}
}

func TestProjectIsTheDominantDirectory(t *testing.T) {
	// A session that wanders into a subdirectory still belongs to its project.
	dir := t.TempDir()
	body := `{"type":"user","sessionId":"w","timestamp":"2026-07-04T08:00:00.000Z","cwd":"/home/me/big-project","message":{"role":"user","content":"first prompt here"}}
{"type":"assistant","sessionId":"w","timestamp":"2026-07-04T08:00:05.000Z","cwd":"/home/me/big-project","message":{"role":"assistant","content":[{"type":"text","text":"one"}]}}
{"type":"assistant","sessionId":"w","timestamp":"2026-07-04T08:00:06.000Z","cwd":"/home/me/big-project/sub","message":{"role":"assistant","content":[{"type":"text","text":"two"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "w.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := collect(t, dir, false, false, 2)["w"]
	if !strings.HasPrefix(c.Title, "big-project — ") {
		t.Errorf("title = %q, want the project it spent most events in", c.Title)
	}
}

func TestFilters(t *testing.T) {
	cases := []struct {
		name string
		f    filter
		want bool
	}{
		{"no filters", filter{}, true},
		{"project matches", filter{only: stringList{"orbit"}}, true},
		{"project does not match", filter{only: stringList{"taxes"}}, false},
		{"excluded", filter{exclude: stringList{"orbit-weaver"}}, false},
		{"exclude misses", filter{exclude: stringList{"unrelated"}}, true},
		{"case insensitive", filter{only: stringList{"ORBIT"}}, true},
		{"after cutoff", filter{cutoff: mustDay(t, "2026-01-01")}, true},
		{"before cutoff", filter{cutoff: mustDay(t, "2026-12-01")}, false},
	}
	c := conv{Title: "orbit-weaver — something", Created: "2026-07-02T10:00:00.000Z", project: "/home/me/orbit-weaver"}
	for _, tc := range cases {
		if got := tc.f.allows(c); got != tc.want {
			t.Errorf("%s: allows = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPruneSnapshotKeepsUnmatchedAndPreservesFields(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "snap.json")
	// Fields the app writes that we do not model must survive the round trip.
	snap := `{"app":"constellate","exported":"2026-07-05T00:00:00Z","conversations":[
	  {"id":"claude:a","provider":"claude","title":"Sourdough starter","messages":[{"role":"user","text":"hi"}],"words":2,"msgCount":1},
	  {"id":"claude-code:b","provider":"claude-code","title":"tax-scripts — quarterly filing","messages":[{"role":"user","text":"hi"}],"words":2},
	  {"id":"claude:c","provider":"claude","title":"Pandas merge","messages":[{"role":"user","text":"hi"}]}]}`
	if err := os.WriteFile(in, []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "pruned.json")
	if err := pruneSnapshot(in, out, filter{exclude: stringList{"tax-scripts"}}); err != nil {
		t.Fatalf("pruneSnapshot: %v", err)
	}

	var got looseSnapshot
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Conversations) != 2 {
		t.Fatalf("kept %d conversations, want 2", len(got.Conversations))
	}
	if got.App != "constellate" {
		t.Errorf("app = %q, want constellate so the app still recognises it", got.App)
	}
	body := string(data)
	if strings.Contains(body, "tax-scripts") {
		t.Error("pruned conversation is still present")
	}
	if !strings.Contains(body, `"msgCount":1`) {
		t.Error("dropped a field the app wrote; prune must not rewrite conversations")
	}

	// Refusing a no-op is what tells the user their filter did not match.
	if err := pruneSnapshot(in, out, filter{exclude: stringList{"nothing-matches-this"}}); err == nil {
		t.Error("pruneSnapshot with no matches should report that nothing was removed")
	}
}

// Constellate's own snapshots store created, updated and every message ts as
// epoch milliseconds. Modelling those as strings made each conversation fail to
// parse, and since unparseable conversations are kept, -prune quietly did
// nothing at all.
func TestPruneReadsTheFormatTheAppActuallyWrites(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "snap.json")
	snap := `{"app":"constellate","exported":"2026-07-05T00:00:00Z","conversations":[
	  {"id":"demo:23","provider":"claude","model":"claude-opus-4","title":"Why is my sourdough dense and gummy",
	   "created":1758401987221,"updated":1758402467221,"msgCount":2,"words":9,
	   "messages":[{"role":"user","text":"dense crumb again","ts":1758401987221},
	               {"role":"assistant","text":"underproofed","ts":1758402467221}]},
	  {"id":"demo:24","provider":"claude","title":"Pandas merge duplicates rows",
	   "created":1758401987221,"updated":1758402467221,
	   "messages":[{"role":"user","text":"row count grew","ts":1758401987221}]}]}`
	if err := os.WriteFile(in, []byte(snap), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "pruned.json")
	if err := pruneSnapshot(in, out, filter{exclude: stringList{"sourdough"}}); err != nil {
		t.Fatalf("pruneSnapshot: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sourdough") {
		t.Error("numeric timestamps stopped the filter from matching")
	}
	if !strings.Contains(string(data), "Pandas merge") {
		t.Error("dropped a conversation the filter did not match")
	}

	// The -since cutoff has to read those numbers too.
	c := conv{Title: "x", Created: float64(1758401987221)} // 2025-09-20
	if (filter{cutoff: mustDay(t, "2026-01-01")}).allows(c) {
		t.Error("-since did not recognise an epoch-millisecond timestamp")
	}
	if !(filter{cutoff: mustDay(t, "2025-01-01")}).allows(c) {
		t.Error("-since wrongly excluded a conversation newer than the cutoff")
	}
}

func TestCleanTextStripsMachinery(t *testing.T) {
	cases := map[string]string{
		"plain prompt": "plain prompt",
		"before<system-reminder>noise</system-reminder>": "before",
		"<command-name>/loop</command-name>run it":       "run it",
		"<system-reminder>unterminated and then some":    "",
		"[Image #3]":   "",
		"   spaced   ": "spaced",
	}
	for in, want := range cases {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func textOf(c conv) string {
	var b strings.Builder
	for _, m := range c.Messages {
		b.WriteString(m.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func keys(m map[string]conv) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
