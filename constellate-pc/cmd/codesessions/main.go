// Export-CodeSessions turns Claude Code session transcripts into a Constellate
// snapshot, so coding sessions land on the same map as chat history.
//
// Claude Code writes one JSONL file per session under ~/.claude/projects, and
// each line is a single event — not a conversation. Constellate's own .jsonl
// path expects one conversation per line with a messages array, so it silently
// imports nothing from these files. This rewrites them into the universal
// snapshot schema Constellate does understand.
//
// It also prunes an existing snapshot (-prune), which is the only way to drop
// conversations from a map: the app can erase everything or nothing.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Only the fields we need; a transcript carries a good deal more.
type event struct {
	Type             string   `json:"type"`
	SessionID        string   `json:"sessionId"`
	Timestamp        string   `json:"timestamp"`
	Cwd              string   `json:"cwd"`
	GitBranch        string   `json:"gitBranch"`
	IsSidechain      bool     `json:"isSidechain"`
	IsMeta           bool     `json:"isMeta"`
	IsCompactSummary bool     `json:"isCompactSummary"`
	Message          *message `json:"message"`
	Summary          string   `json:"summary"`
}

type message struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"` // string or []block
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// The universal snapshot schema Constellate imports.
type snapshot struct {
	App           string `json:"app"`
	Exported      string `json:"exported"`
	Conversations []conv `json:"conversations"`
}

type conv struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Title    string `json:"title"`
	Model    string `json:"model"`
	// Constellate writes these as epoch milliseconds in its own snapshots while
	// transcripts carry RFC3339 strings, and a snapshot that fails to parse here
	// would be silently kept when pruning. Accept whichever form turns up.
	Created  any       `json:"created"`
	Updated  any       `json:"updated"`
	Messages []convMsg `json:"messages"`

	// Where the session ran. Unexported, so it is never written out and never
	// read back from a snapshot — pruning falls back to matching on the title.
	project string
}

type convMsg struct {
	Role string `json:"role"`
	Text string `json:"text"`
	// Epoch milliseconds in the app's snapshots, RFC3339 in transcripts.
	TS any `json:"ts"`
}

// A snapshot being pruned is only partly ours, so keep every field the app
// wrote and filter at the conversation level.
type looseSnapshot struct {
	App           string            `json:"app"`
	Exported      string            `json:"exported"`
	Conversations []json.RawMessage `json:"conversations"`
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var only, exclude stringList
	out := flag.String("out", "claude-code-sessions.json", "file to write")
	root := flag.String("root", "", "transcript directory (default: your Claude Code session folders)")
	list := flag.Bool("list", false, "list the projects found and write nothing")
	since := flag.String("since", "", "skip sessions older than this date (YYYY-MM-DD)")
	minMsgs := flag.Int("min-messages", 2, "skip sessions with fewer messages than this")
	provider := flag.String("provider", "claude-code", "provider name to file these under in Constellate")
	includeTools := flag.Bool("include-tools", false, "include the names of tools each session used")
	includeSide := flag.Bool("include-sidechains", false, "include subagent transcripts as part of their session")
	prune := flag.String("prune", "", "filter an existing Constellate snapshot instead of reading transcripts")
	flag.Var(&only, "project", "only projects whose path contains this (repeatable)")
	flag.Var(&exclude, "exclude", "skip projects whose path contains this (repeatable)")
	flag.Parse()

	// Keep the window up long enough to read when the exe was double-clicked.
	defer waitForEnterIfInteractive()

	var cutoff time.Time
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			fail("-since must look like 2026-07-29: %v", err)
		}
		cutoff = t
	}
	keep := filter{only: only, exclude: exclude, cutoff: cutoff}

	if *prune != "" {
		// Pruning writes a snapshot, not a session export, so give it a name that
		// says so unless one was asked for explicitly.
		explicitOut := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "out" {
				explicitOut = true
			}
		})
		if !explicitOut {
			*out = "constellate-pruned.json"
		}
		if err := pruneSnapshot(*prune, *out, keep); err != nil {
			fail("%v", err)
		}
		return
	}

	roots, err := transcriptRoots(*root)
	if err != nil {
		fail("%v", err)
	}
	files := findTranscripts(roots)
	if len(files) == 0 {
		fail("No Claude Code transcripts found in:\n  %s\n\n"+
			"Point -root at the folder holding your .jsonl session files if they live elsewhere.",
			strings.Join(roots, "\n  "))
	}
	fmt.Printf("Reading %d transcript file(s) from %s\n", len(files), strings.Join(roots, ", "))

	sessions := map[string]*session{}
	for _, f := range files {
		if err := readTranscript(f, sessions, *includeSide, *includeTools); err != nil {
			fmt.Fprintf(os.Stderr, "  skipping %s: %v\n", filepath.Base(f), err)
		}
	}

	convs := make([]conv, 0, len(sessions))
	skipped := 0
	for _, s := range sessions {
		c, ok := s.toConv(*provider, *minMsgs)
		if !ok {
			skipped++
			continue
		}
		if !keep.allows(c) {
			skipped++
			continue
		}
		convs = append(convs, c)
	}
	sort.Slice(convs, func(i, j int) bool {
		a, _ := timeOf(convs[i].Created)
		b, _ := timeOf(convs[j].Created)
		return a.Before(b)
	})

	if *list {
		printProjects(convs)
		fmt.Printf("\n%d session(s) would be exported, %d skipped.\n", len(convs), skipped)
		fmt.Println("Re-run with -exclude <text> to leave a project out.")
		return
	}
	if len(convs) == 0 {
		fail("Every session was filtered out — nothing to write.")
	}
	if err := writeSnapshot(*out, convs); err != nil {
		fail("%v", err)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Printf("\nWrote %d session(s) to %s\n", len(convs), abs)
	if skipped > 0 {
		fmt.Printf("Skipped %d session(s) (too short, or filtered out).\n", skipped)
	}
	fmt.Println("Drag that file onto the Constellate window to put these on the map.")
}

/* ---------------- reading transcripts ---------------- */

type session struct {
	id string
	// A session's working directory can change as it runs, so the project is the
	// directory it spent most of its events in rather than wherever it ended up.
	cwds     map[string]int
	firstCwd string
	branch   string
	models   map[string]int
	first    string
	last     string
	messages []convMsg
	summary  string
}

// readTranscript folds one JSONL file into the session map. Sessions can span
// files when they are resumed, so events are grouped by sessionId rather than by
// filename.
func readTranscript(path string, into map[string]*session, includeSide, includeTools bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Transcript lines carry whole tool results and can be very large.
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	fallbackID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e event
		if json.Unmarshal([]byte(line), &e) != nil {
			continue // a partially written last line is normal for a live session
		}
		if e.IsSidechain && !includeSide {
			continue
		}
		id := e.SessionID
		if id == "" {
			id = fallbackID
		}

		s := into[id]
		if s == nil {
			s = &session{id: id, models: map[string]int{}, cwds: map[string]int{}}
			into[id] = s
		}
		if e.Cwd != "" {
			s.cwds[e.Cwd]++
			if s.firstCwd == "" {
				s.firstCwd = e.Cwd
			}
		}
		if e.GitBranch != "" {
			s.branch = e.GitBranch
		}
		// A /compact summary is the best title a session has.
		if e.Type == "summary" && e.Summary != "" && s.summary == "" {
			s.summary = e.Summary
		}
		if e.Type != "user" && e.Type != "assistant" {
			continue // attachment, system, queue-operation, last-prompt: not conversation
		}
		if e.Message == nil || e.IsMeta || e.IsCompactSummary {
			continue
		}
		if e.Message.Model != "" {
			s.models[e.Message.Model]++
		}
		text := extractText(e.Message.Content, includeTools)
		if text == "" {
			continue
		}
		role := "assistant"
		if e.Message.Role == "user" {
			role = "user"
		}
		s.messages = append(s.messages, convMsg{Role: role, Text: text, TS: e.Timestamp})
		if e.Timestamp != "" {
			if s.first == "" || e.Timestamp < s.first {
				s.first = e.Timestamp
			}
			if e.Timestamp > s.last {
				s.last = e.Timestamp
			}
		}
	}
	return sc.Err()
}

// extractText pulls the human-meaningful prose out of a message. Tool call
// arguments and tool results are dropped: they are mostly file contents and
// command output, and they would dominate the topic model that decides where a
// session sits on the map.
func extractText(content json.RawMessage, includeTools bool) string {
	if len(content) == 0 {
		return ""
	}
	var asString string
	if json.Unmarshal(content, &asString) == nil {
		return cleanText(asString)
	}
	var blocks []block
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	var tools []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if t := cleanText(b.Text); t != "" {
				parts = append(parts, t)
			}
		case "tool_use":
			if includeTools && b.Name != "" {
				tools = append(tools, b.Name)
			}
		}
		// thinking, tool_result, image and friends are deliberately ignored.
	}
	if len(tools) > 0 {
		parts = append(parts, "[tools: "+strings.Join(dedupe(tools), ", ")+"]")
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// cleanText strips the machinery Claude Code wraps around prompts so it does not
// end up as conversation topic signal.
func cleanText(s string) string {
	for _, tag := range []string{"system-reminder", "command-name", "command-message", "command-args",
		"local-command-stdout", "local-command-stderr", "user-prompt-submit-hook"} {
		s = stripTag(s, tag)
	}
	s = strings.TrimSpace(s)
	// Pasted-file and image placeholders carry no topic signal on their own.
	if strings.HasPrefix(s, "[Image #") || strings.HasPrefix(s, "[Request interrupted") {
		return ""
	}
	return s
}

func stripTag(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], close)
		if j < 0 {
			return strings.TrimSpace(s[:i]) // unterminated: drop the rest
		}
		s = s[:i] + s[i+j+len(close):]
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// toConv turns a gathered session into a Constellate conversation.
func (s *session) toConv(provider string, minMsgs int) (conv, bool) {
	if len(s.messages) < minMsgs {
		return conv{}, false
	}
	cwd := s.mainCwd()
	project := projectName(cwd)
	title := s.summary
	if title == "" {
		for _, m := range s.messages {
			if m.Role == "user" {
				title = firstLine(m.Text)
				break
			}
		}
	}
	if title == "" {
		title = "session " + shortID(s.id)
	}
	if project != "" {
		title = project + " — " + title
	}

	model, best := "", 0
	for m, n := range s.models {
		if n > best {
			model, best = m, n
		}
	}
	return conv{
		ID:       provider + ":" + s.id,
		Provider: provider,
		Title:    truncate(title, 110),
		Model:    model,
		Created:  s.first,
		Updated:  s.last,
		Messages: s.messages,
		project:  cwd,
	}, true
}

// mainCwd is the directory the session spent most of its events in, falling back
// to the first one seen when there is a tie.
func (s *session) mainCwd() string {
	best, bestN := s.firstCwd, 0
	for cwd, n := range s.cwds {
		if n > bestN || (n == bestN && cwd == s.firstCwd) {
			best, bestN = cwd, n
		}
	}
	return best
}

func projectName(cwd string) string {
	if cwd == "" {
		return ""
	}
	cwd = strings.TrimRight(strings.ReplaceAll(cwd, `\`, "/"), "/")
	if i := strings.LastIndex(cwd, "/"); i >= 0 {
		return cwd[i+1:]
	}
	return cwd
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

/* ---------------- filtering ---------------- */

type filter struct {
	only    stringList
	exclude stringList
	cutoff  time.Time
}

func (f filter) allows(c conv) bool {
	// Match on the project path when we know it, else on the title, so the same
	// flags work for pruning a snapshot of chat conversations.
	subject := c.project
	if subject == "" {
		subject = c.Title
	}
	subject = strings.ToLower(subject)
	if len(f.only) > 0 {
		hit := false
		for _, want := range f.only {
			if strings.Contains(subject, strings.ToLower(want)) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, drop := range f.exclude {
		if strings.Contains(subject, strings.ToLower(drop)) {
			return false
		}
	}
	if !f.cutoff.IsZero() {
		if t, ok := timeOf(c.Created); ok && t.Before(f.cutoff) {
			return false
		}
	}
	return true
}

// timeOf reads a timestamp in either of the forms a conversation can carry.
func timeOf(v any) (time.Time, bool) {
	switch t := v.(type) {
	case string:
		if t == "" {
			return time.Time{}, false
		}
		parsed, err := time.Parse(time.RFC3339, t)
		return parsed, err == nil
	case float64:
		if t <= 0 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(t)).UTC(), true
	default:
		return time.Time{}, false
	}
}

/* ---------------- output ---------------- */

func writeSnapshot(path string, convs []conv) error {
	snap := snapshot{App: "constellate", Exported: time.Now().UTC().Format(time.RFC3339), Conversations: convs}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// pruneSnapshot rewrites a snapshot exported from Constellate with the matching
// conversations removed. Import it after "Erase all local data" to get a map
// without them — the app has no way to delete a single conversation.
func pruneSnapshot(in, out string, keep filter) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return fmt.Errorf("reading %s: %w", in, err)
	}
	var loose looseSnapshot
	if err := json.Unmarshal(data, &loose); err != nil {
		return fmt.Errorf("%s is not a Constellate snapshot: %w", in, err)
	}
	if len(loose.Conversations) == 0 {
		return fmt.Errorf("%s has no conversations in it", in)
	}

	kept := make([]json.RawMessage, 0, len(loose.Conversations))
	var dropped []string
	unreadable := 0
	for _, raw := range loose.Conversations {
		var c conv
		if json.Unmarshal(raw, &c) != nil {
			// Keep rather than lose data, but count it: a filter that appears to
			// match nothing because every conversation failed to parse is a bug,
			// not an empty result.
			kept = append(kept, raw)
			unreadable++
			continue
		}
		if keep.allows(c) {
			kept = append(kept, raw)
		} else {
			dropped = append(dropped, c.Title)
		}
	}
	if unreadable > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d of %d conversations could not be read and were kept\n",
			unreadable, len(loose.Conversations))
	}
	if len(dropped) == 0 {
		return fmt.Errorf("no conversations matched the filters — nothing was removed")
	}

	loose.Conversations = kept
	blob, err := json.Marshal(loose)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, blob, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	abs, _ := filepath.Abs(out)
	fmt.Printf("Removed %d conversation(s), kept %d.\n", len(dropped), len(kept))
	for i, t := range dropped {
		if i == 10 {
			fmt.Printf("  … and %d more\n", len(dropped)-10)
			break
		}
		fmt.Printf("  - %s\n", t)
	}
	fmt.Printf("\nWrote %s\n", abs)
	fmt.Println("In Constellate: Export ▾ → Erase all local data, then drag this file onto the window.")
	return nil
}

func printProjects(convs []conv) {
	type row struct {
		n           int
		first, last time.Time
	}
	byProject := map[string]*row{}
	var order []string
	for _, c := range convs {
		name := projectName(c.project)
		if name == "" {
			name = "(unknown project)"
		}
		created, _ := timeOf(c.Created)
		updated, _ := timeOf(c.Updated)
		r := byProject[name]
		if r == nil {
			r = &row{first: created, last: updated}
			byProject[name] = r
			order = append(order, name)
		}
		r.n++
		if !created.IsZero() && (r.first.IsZero() || created.Before(r.first)) {
			r.first = created
		}
		if updated.After(r.last) {
			r.last = updated
		}
	}
	sort.Slice(order, func(i, j int) bool { return byProject[order[i]].n > byProject[order[j]].n })

	fmt.Println("\nProject                          Sessions  First        Last")
	for _, name := range order {
		r := byProject[name]
		fmt.Printf("%-32s %8d  %-11s  %s\n", truncate(name, 32), r.n, day(r.first), day(r.last))
	}
}

func day(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.Format("2006-01-02")
}

/* ---------------- locating transcripts ---------------- */

// transcriptRoots mirrors the folders Claude-AutoResume.ps1 scans, so both tools
// agree on where sessions live.
func transcriptRoots(override string) ([]string, error) {
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return nil, fmt.Errorf("-root %s: %w", override, err)
		}
		return []string{override}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find your home directory: %w", err)
	}
	candidates := []string{filepath.Join(home, ".claude", "projects")}
	if appData := os.Getenv("APPDATA"); appData != "" {
		// Some desktop-app builds keep sessions here instead of ~/.claude.
		candidates = append(candidates, filepath.Join(appData, "Claude", "claude-code-sessions"))
	}
	var roots []string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			roots = append(roots, c)
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("no Claude Code session folder found (looked in %s)", strings.Join(candidates, " and "))
	}
	return roots, nil
}

func findTranscripts(roots []string) []string {
	var out []string
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".jsonl") {
				out = append(out, path)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "\n"+format+"\n", args...)
	waitForEnterIfInteractive()
	os.Exit(1)
}

// waitForEnterIfInteractive holds a double-clicked window open so its output can
// be read. Running with any argument means a real console, where pausing would
// just be in the way.
func waitForEnterIfInteractive() {
	if runtime.GOOS != "windows" || len(os.Args) > 1 {
		return
	}
	fmt.Print("\nPress Enter to close…")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
