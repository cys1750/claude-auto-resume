# Reports to file upstream

Most of the patches in this directory fix bugs in
[Constellate](https://github.com/JCarterJohnson/constellate) itself, not in the
Windows packaging. They are written up below so they can be filed as issues —
copy a section as-is. All were found against revision
`31508ea5630b4404409b468fbab249126ab6c18f`.

`0003-load-offered-snapshot` is deliberately not here: it exists to support this
launcher's `-snapshot` flag and the phone bundle, and upstream has no reason to
want it.

---

## The 3D map is blank until you touch a control

**Where:** `index.html`, `rebuildAll()`

Loading the demo universe, or importing any export, leaves the viewport empty.
The sidebar fills in correctly — conversation count, provider list, topic
clusters, timeline — so it looks like a WebGL or driver problem, but the graph is
simply never painted.

`rebuildAll()` finishes with `rebuildNodes()`, `buildTimeline()`, `updateCounts()`
and `reheat(1)`, and never calls `draw()`. `draw()` is what writes the `aColor`,
`aSize` and `aAlpha` attributes and the `G.vis` visibility flags. Until it runs,
every node has size 0 and alpha 0 — the fragment shader's `if (a < 0.02) discard`
throws them all away — and `tickSim()` skips every node too, since it only walks
indices where `G.vis[i]` is set.

Moving the mouse does not recover it: `pick()` tests against nodes that are
invisible but still present, so `S.hovered` does change on a lucky hit, but until
then nothing calls `draw()`. Toggling **Labels** or changing **Color by** calls
`draw()` directly, and the entire map appears at once — that is the tell.

Measured in Chromium, immediately after **Load demo universe** with no
interaction:

| | visible nodes | lit pixels |
| --- | --- | --- |
| before | 0 / 150 | 0 |
| after `draw()` | 150 / 150 | ~20,000 |

**Fix**

```diff
   buildStats();
   buildTimeline();
   updateCounts();
+  draw();
   reheat(1);
```

Placing it before `reheat(1)` matters: the simulation needs `G.vis` populated on
its first tick.

---

## Touchscreens can rotate the map but not zoom or pan

**Where:** `index.html`, the camera controls after `/* ---- camera controls +
picking ---- */`

On a phone or tablet the map can be orbited and nothing else. You cannot get
closer to a node or move off centre, which makes the map unusable at any real
conversation count.

The canvas already uses pointer events with `touch-action: none`, so one-finger
orbiting works. The other two gestures have no touch equivalent:

- zoom lives only in the `wheel` listener, and touch produces no wheel events;
- panning requires `e.button === 2 || e.shiftKey || e.metaKey`, none of which a
  touch pointer reports.

**Fix** — the full patch is
[`0002-touch-gestures.patch`](0002-touch-gestures.patch). It tracks live pointers
in a `Map`, and when a second one goes down it switches from orbiting to
pinch-zoom (scaling `R.cam.r` by the ratio of finger distances) plus a pan driven
by the midpoint, reusing the existing pan maths. Two details worth keeping:

- a second finger sets `pointer.moved = true`, so a pinch is never mistaken for a
  tap that selects a conversation;
- lifting back to one finger re-seeds `pointer.sx/sy` from the remaining finger,
  otherwise the map jumps by however far apart the two fingers were.

`pointercancel` is handled next to `pointerup`, since the browser can take a
gesture away mid-flight.

Verified with synthetic two-finger gestures in Chromium: pinching out took the
camera radius from 843 to 248, pinching in took it back to 2109, a two-finger
drag moved `R.cam.target` while leaving the radius untouched, one finger still
orbited, and no conversation was selected by accident.

---

## Clicking a node misses it, and the map is drawn slightly stretched

**Where:** `index.html`, `rebuildAll()` and `R.resize()`

Clicking or tapping a conversation usually does nothing, and node labels sit a
little below the nodes they belong to. Both come from `CW`/`CH` describing a
canvas taller than the one on screen.

`R.resize()` reads them from the canvas, and runs at boot, on window resize and
when the sidebar is toggled. `rebuildAll()` then reveals the timeline with
`$("timeline").style.display = "block"`, which shortens the canvas — and nothing
re-measures. The timeline is shown whenever there is data, so every session with
conversations in it runs with stale dimensions.

Two consequences:

- `pick()` maps a pointer to normalised device coordinates by dividing by the
  stale `CH`, so the ray is aimed somewhere other than the pointer. Measured at
  1280x820: `CH` was 770 against a canvas 708 tall, and a ray aimed at a node's
  projected centre passed **79 world units** away from it, against a pick
  threshold of ~12. Hovering and `R.project()`'s label placement are off by the
  same margin.
- `R.renderer.setSize(CW, CH, false)` keeps the drawing buffer and camera aspect
  at the taller shape (aspect 1.339 against the element's 1.456), so the frame is
  drawn stretched vertically and scaled back down by the browser.

It reads as "clicking nodes does nothing" rather than as a sizing bug, since the
offset is only ~9% of the height: the map still looks plausible and the labels
look merely untidy.

**Fix**

```diff
   $("empty").style.display = S.convs.length ? "none" : "flex";
   $("timeline").style.display = S.convs.length ? "block" : "none";
+  R.resize();
```

After it, `CH` matches the canvas exactly, the camera aspect matches the element's,
and a tap on a node opens its conversation. A `ResizeObserver` on the canvas would
be the more general fix if other layout changes are planned.

---

## The layout does not fit a phone

**Where:** `index.html`, the `#side` and `#topbar` rules

There are no media queries, and two fixed-width choices make the app awkward on a
touch device. Numbers below are from a 412px-wide viewport (a Galaxy S23 Ultra in
Chrome).

- `#side` is a fixed `248px`, so the map is left with 164px of a 412px screen. The
  header toggle collapses it, but a first-time visitor lands on mostly filter
  controls. `patches/0004` starts it collapsed below 700px, which is the initial
  state only.
- `#topbar` is one flex row holding the logo and its tagline, search, the totals,
  Import, Export and Help. The tagline wraps onto three lines, and `#counts` —
  `white-space:nowrap` with `margin-left:auto` — pushes Export to 383->443 and Help
  to 449->465, both past the 412px edge. `patches/0006` hides the tagline and the
  totals below 700px, which fits everything on one line; the sidebar already lists
  counts per provider.

Both are initial-state or breakpoint-only changes, with nothing altered above
700px.

---

## Smaller note: `.jsonl` accepts a shape Claude Code does not write

Not a patch, just something worth knowing. The file picker accepts `.jsonl`, and
`parseFile()` treats each line as a conversation that must have a `messages`
array. Claude Code transcripts (`~/.claude/projects/**/*.jsonl`) are one *event*
per line — `{"type":"user","message":{...},"sessionId":...}` — so every line fails
the check and the import silently yields nothing.

Grouping events by `sessionId` would make Claude Code sessions importable
directly. `Export-CodeSessions.exe` in this repository does that conversion
externally and emits the universal snapshot schema instead.
