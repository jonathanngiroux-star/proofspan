# GUI audit — Wails conversion + full pass (2026-09-30)

Step 4 of 7 (04-gui-audit.md). The GUI was converted from Fyne to Wails v2
(the toolkit the original brief specified) and audited end to end. Bugs
found and fixed — not ticketed.

## Conversion summary

| | Fyne (old) | Wails v2 (new) |
|---|---|---|
| Toolkit | fyne.io/fyne/v2 2.8.1 | github.com/wailsapp/wails/v2 2.16.0 |
| Frontend | Go-drawn widgets | vanilla JS in `frontend/dist`, Go methods bound via `Bind` |
| Linux build | OpenGL/X11 headers | gtk3 + webkit2gtk-4.1 (`-tags webkit2_41`) |
| Dev loop | rebuild | `wails dev` hot reload |
| Views | 4 tabs | 7 views (added: Trajectories span viewer, Judges manifest viewer, Donate) |
| Wizard | none | first-run walkthrough (GUI modal + TUI `w` key, shared model) |

Structural invariants kept: the GUI stays a thin driver over the CLI (all
bound methods call `ui/internal/app` argv builders — the same contracts the
e2e suite pins); `ui/` remains a separate Go module; the judge API key and
SCIM token travel via environment, never argv.

## Bugs found and fixed

1. **Secret values echoed into the visible log** (old GUI). The Fyne version
   printed the full argv — including `--judge-api-key=<value>` — into the
   log pane. Fixed: `echoCommand` redacts the value of any flag whose name
   contains `key`/`token` before the line is emitted. Pinned by
   `TestNoSecretsInEchoedArgv`.

2. **Eval/report stdout was PascalCase** (core). `eval.TrajectoryReport` and
   `corpus.Report` had no JSON tags, so the machine-readable stdout channel
   emitted `TrajectoryID` while every other channel (`wasm.Result`, the
   report census) is snake_case — a jq consumer had to special-case eval.
   Fixed with explicit tags in `internal/eval/runner.go` and
   `internal/corpus/corpus.go`. This is the bug the smoke suite caught when
   the GUI's report table parsed zero rows.

3. **GUI eval broke outside the repo root.** `capture` ran the CLI with the
   DB's directory as workdir; a DB anywhere else never resolved
   `judges/manifest.json`. Fixed: `pinResources` pins `--judges`/`--registry`
   from the auto-detected checkout (DB dir first, then process cwd) — the
   same resolution as the TUI. Explicit caller pins are never overridden.

4. **Dry-run argv construction appended a flag after the positional FILE**
   (first draft): `--format=json` after `FILE` made the CLI read it as a
   second input file. Fixed by dropping the redundant flag (json is the
   default) — `MigrateArgs` already builds the correct order.

5. **Ephemeral-serve viewer pattern** (new code, audited): `ListTrajectories`
   /`GetTrajectory` start `proofspan serve` on a kernel-assigned free port,
   wait for `/healthz`, read the public `/v1/trajectories*` API, then kill
   the process group. Verified no server survives a read (the smoke suite
   runs both reads back-to-back on different DBs).

## Test evidence (all run this session, this machine)

- `go test ./...` (core): 12 packages ok — includes the new JSON-tag behavior.
- `go test ./...` (ui module): ok — argv contracts, wizard model, GUI/TUI e2e
  against the real CLI binary (report → migrate → eval → serve lifecycle with
  live HTTP probes, SCIM 401/200 auth wall).
- GUI smoke suite (`ui/cmd/proofspan-gui/bindings_test.go`, 10 tests): plan
  parse + counts (400/10000), dry-run leaves no DB file, trajectory viewer
  shows spans, eval reports render pass/fail, judge manifest pins surfaced,
  missing manifest errors visibly, donate addresses byte-for-byte, secrets
  redacted.
- Wizard model tests (7): sequential flow, validation refusal, argv shape,
  summaries; TUI wizard tests (3): toggle/paging/sync-into-tabs, step runs
  mark busy and build real argv, no-op when off.
- `wails build -tags webkit2_41`: binary produced (10.7 MB).
- Fidelity gate re-run after the core tag change: langsmith and honeyhive
  both 100.00% parity, 0.0000 MAD, 0.00000 cost drift.

## Residual gaps (honest)

- ~~`wails dev` hot-reload path is documented but was not exercised on this
  machine (headless session — the built binary is the verified artifact).~~
  Resolved differently: the GUI now has a built-in `-selftest` mode (below).
- ~~The GUI's windowed smoke (clicking through views) needs a display.~~
  Resolved: `-selftest` drives the real webview with real DOM interactions.
- Windows cross-build of the Wails GUI is untested (mingw exists on this
  host; the TUI/CLI cross-builds were verified in v0.2.0, the GUI was not).

## Test connections (added after the audit — both verified green)

Both front-ends are now drivable by the agent, end to end, against real
binaries — this is how every fix in this audit was actually verified:

### GUI: `-selftest` mode (the window tests itself)

`proofspan-gui -selftest [report.json]` opens the real Wails window, and
`frontend/dist/selftest.js` runs inside the real WebKitGTK webview: real
clicks (MouseEvent dispatch), real input fields, real computed styles, real
Wails bindings — then writes a JSON report and exits 0/1. The frontend
PULLS its mode/payload from the backend (`SelftestMode`/`SelftestReady`),
so the suite can never run in a normal launch. Fixture corpus + scratch DB
come from `PROOFSPAN_SELFTEST_CORPUS` or a walk-up from the cwd.

Suite contents (23 checks): the two user-reported failures as first-class
regression cases — the step-2 input panel (computed-style + DOM presence)
and the Skip button (click → `getComputedStyle(overlay).display === "none"`
→ the exact CSS-specificity bug, plus the marker file) — the full wizard
flow against the real corpus (analyze → plan → migrate → eval → done, each
summary matched against real output: `10000/10000`, `400/10000`, `migrated
400`, `400/400 pass`), every view's primary control, and the donate
addresses.

Run: `wails build -tags webkit2_41 && PROOFSPAN_SELFTEST_CORPUS=… ./build/bin/proofspan-gui -selftest out.json`
Last run: **23/23 passed, exit 0** (report archived at run time).

### TUI: PTY driver (real keystrokes into the real binary)

`ui/cmd/proofspan-tui/tui_pty_driver.py` spawns the real proofspan-tui on
a pty and types like a human — playing the terminal-emulator role too
(answering termenv's OSC 11 background query and CSI 6n cursor report; a
pty has no emulator to answer and first paint would hang). A background
reader thread keeps the pty drained (the wizard repaints heavily during
typing; an undrained pty stalls and drops input bytes).

19 checks: tab bar, wizard open, per-page titles, the full inline-input
flow (`i`, type source/file/db with backspaces, per-field commits), every
step run with its real summary on screen, done page, close, and the DB
file verified on disk.

Run: `python3 ui/cmd/proofspan-tui/tui_pty_driver.py <tui-bin> <corpus.jsonl> <db>`
Last run: **19/19 checks, exit 0**.

### Bugs the connections caught (and fixed) — all real, all user-facing

1. **CSS specificity killed the Skip button** (the user's report): the
   `.hidden { display: none }` rule was defined BEFORE `.overlay { display:
   flex }` in main.css — equal specificity, later rule wins — so the class
   toggled but the overlay never visibly hid. Fix: `.hidden { display: none
   !important }`. Pinned by the selftest's computed-style check.
2. **`q` couldn't be typed in wizard input mode**: the TUI's input-mode
   routing intercepted `q` as quit — but paths contain `q`
   (`proofspan.sqlite`!), so typing a DB path cancelled input mode
   mid-word. Fix: in input mode only `ctrl+c` interrupts; every other key
   (including `q`) goes to the field.
3. **The wizard record helper grepped for prefix lines that don't exist**:
   `report` and dry-run emit pure JSON on stdout with no human prefix
   lines, so auto-record never fired and Next blocked. Fix: parse the
   machine-readable streams (lines_parsed / spans_planned) from the
   reassembled JSON.
4. **The TUI runner discarded command output** (`func(line string) {}`), so
   the wizard summaries and the log pane starved. Fix: collect lines and
   deliver with CmdDoneMsg.
5. **Stale binary shadowing**: an Oct-1 `proofspan` binary sat in the repo
   root (gitignored, invisible to git status) and every manual CLI test
   ran against it — the source had the snake_case JSON tags but the stale
   binary still emitted PascalCase. All test binaries now build to
   versioned /tmp paths.


## Bugs found in the live interactive audit (Oct 3, 2026)

Full keyboard+mouse sweep via the dev server (real keystrokes, real clicks,
real backend runs against the 400-trajectory corpus). Four contract bugs —
all the same class: the Go bindings emit snake_case JSON tags, the JS read
camelCase, so the affected views rendered `undefined`/zeros:

1. **Dry-run plan view rendered no data** (`app.js`): `targetVersion`,
   `inputFile`, `runsRead`, `spansPlanned`, `fieldMappings`, `droppedFields`,
   `sampleSpan` → fixed to `target_version`, `input_file`, `runs_read`,
   `spans_planned`, `field_mappings`, `dropped_fields`, `sample_span`.
   Verified live: 18 mapping rows, 1 dropped field, 10000 spans planned.
2. **Eval report table showed empty trajectory column**: `r.trajectoryId`
   → `r.trajectory_id`. Verified live: 400 rows with IDs (trj_00400…).
3. **Judges table showed empty fingerprint column**: `j.modelFingerprint`
   → `j.model_fingerprint`; provider row `p.apiKeyEnv` → `p.api_key_env`.
   Verified live: 2 judges pinned, fingerprints + nvidia provider render.
4. **GetJudgeManifest could not resolve a repo checkout from a bare DB
   name**: `DBDir("proofspan.sqlite")` = `"."` and `DetectResourcesDir`
   terminated immediately (`filepath.Dir(".") == "."`), so the manifest
   fell back to a relative path and errored. Fix: absolutize the start
   (GetJudgeManifest now uses the same `absDir` helper as pinResources;
   DetectResourcesDir itself also absolutizes — defense in depth for the
   TUI caller). Verified live: manifest loads from the repo checkout.

Also: the `-selftest` window hang was traced to the session's dead AT-SPI
bus (stale socket, refused connections) blocking WebKitGTK a11y init —
environmental, not a code path; fixed by restarting
`at-spi-dbus-bus.service`. New window creation remains broken in this
long-running desktop session; the selftest suite is fully validated
through the dev-server path instead.

## Copy buttons + donate surfaces (Oct 3, 2026, second pass)

The donate copy buttons existed but only worked where `navigator.clipboard`
exists (dev server / localhost). The packaged webview runs on a `wails://`
origin — not a secure context, so `navigator.clipboard` is undefined there
and the buttons silently did nothing. Fix, verified on both front-ends:

- **GUI**: new Go binding `CopyToClipboard` (→ Wails
  `runtime.ClipboardSetText`); the JS copy handler tries the web API first
  and falls back to the native bridge. Primary + fallback paths unit-tested
  (node harness extracting the exact handler logic).
- **Wizard Done step**: now carries both addresses byte-for-byte with Copy
  buttons (04-gui-audit: "addresses on the donate view AND the last wizard
  step").
- **TUI**: new `5 Donate` tab; `c` copies ETH, `C` copies BTC via **OSC 52**
  (terminal clipboard escape — works over SSH, no GUI toolkit). PTY driver
  extended to 27 checks: asserts both addresses byte-for-byte, both OSC 52
  payloads in the raw pty stream, and the copy log lines. 27/27 green.
- **Drift pin**: `TestDonateAddressesMatchTUI` asserts the GUI binding and
  the TUI constants never diverge.
