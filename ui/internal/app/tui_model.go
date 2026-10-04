package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TUIModel is the terminal UI's entire state machine — no terminal IO.
// Elm-style: Update(Msg) mutates state; the renderer reads it each frame.
// All commands are the same argv the GUI/CLI produce (shared builders).
type TUIModel struct {
	tab      Tab
	finished bool

	// per-tab config state
	reportCfg  ReportConfig
	migrateCfg MigrateConfig
	evalCfg    EvalConfig
	serveCfg   ServeConfig

	busy      bool
	pendingID int
	lastCmdID int
	status    string
	log       []string

	serveUp bool

	// wizard mode (w toggles): shared model in wizard.go, rendered as the
	// active view; every step command goes through the same argv builders.
	wizard   *Wizard
	wizardOn bool

	// wizard inline input: i enters input mode; keystrokes edit the active
	// field (source → file → db); enter commits and advances to the next
	// field; esc leaves input mode. This is how a terminal user types the
	// export path inside the wizard.
	wizardInputOn    bool
	wizardInputField int // 0=source 1=file 2=db
	wizardInputBuf   string

	// resourcesDir is the repo checkout root (judges/ + registry/) when
	// detected; eval argv pins --judges/--registry from it.
	resourcesDir string

	// seams (nil = real behavior in the TUI binary)
	fastForward    func(id int) Msg
	runHook        func(Cmd)
	serveStartHook func(ServeConfig) error
	serveStopHook  func() error
}

// Tab identifies the active pane.
type Tab int

// Donate addresses — byte-for-byte the README/GOVERNANCE pair. Optional
// support only; never gated.
const (
	DonateETH = "0x85ee7E71f762d772599cbF1EC20E651B30657521"
	DonateBTC = "bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg"
)

const (
	TabAnalyze Tab = iota
	TabMigrate
	TabEvaluate
	TabServe
	TabDonate
)

// Field identifies an editable config value.
type Field int

const (
	FieldSource Field = iota
	FieldFile
	FieldOutDir
	FieldDB
	FieldDryRun
	FieldTrajectory
	FieldAssertions
	FieldJudges
	FieldJudgeEndpoint
	FieldJudgeAPIKey
	FieldAddr
	FieldSCIM
	FieldSCIMToken
)

// Msg is any event the model consumes.
type Msg interface{}

type TabSelectMsg struct{ Tab Tab }
type FieldMsg struct {
	Field Field
	Value string
}
type RunMsg struct{}
type CmdDoneMsg struct {
	ID    int
	Code  int
	Lines []string
}
type QuitMsg struct{}
type ServeStartMsg struct{}
type ServeStopMsg struct{}
type LogMsg struct{ Line string }

// DonateCopyMsg asks the wrapper to put the given address on the terminal
// clipboard via OSC 52 (works over SSH; no GUI toolkit involved).
type DonateCopyMsg struct{ Text string }

// WizardToggleMsg enters/leaves wizard mode.
type WizardToggleMsg struct{}

// WizardNextMsg / WizardBackMsg page through the wizard.
type WizardNextMsg struct{}
type WizardBackMsg struct{}

// WizardRunMsg executes the current wizard step's command (analyze → plan →
// migrate → eval), same as the GUI's Run this step button.
type WizardRunMsg struct{}

// WizardSetInputsMsg updates the wizard's source/file/db in one event.
type WizardSetInputsMsg struct {
	Source, File, DB string
}

// WizardInputModeMsg enters/leaves inline input for the current wizard
// field sequence (source → file → db → done) — the TUI's way to type the
// export path without a prompt library.
type WizardInputModeMsg struct{}

// WizardInputKeyMsg delivers one keystroke while in wizard input mode.
type WizardInputKeyMsg struct{ Key string }

// WizardStateMsg reports the wizard's state for rendering (test seam: the
// PTY driver reads the rendered pane, but tests can assert on state).
type WizardStateMsg struct {
	On    bool
	Step  WizardStep
	Title string
}

// Cmd is a command execution request handed to the runner.
type Cmd struct {
	Label string
	Args  []string
	Env   map[string]string
	Dir   string
}

// NewTUIModel returns the initial state.
func NewTUIModel() *TUIModel {
	return &TUIModel{
		tab:        TabAnalyze,
		status:     "Ready. Tab: 1-Analyze 2-Migrate 3-Evaluate 4-Serve. q quits.",
		migrateCfg: MigrateConfig{Source: "langsmith", DB: "proofspan.sqlite"},
		reportCfg:  ReportConfig{Source: "langsmith"},
		evalCfg:    EvalConfig{DB: "proofspan.sqlite"},
		serveCfg:   ServeConfig{DB: "proofspan.sqlite", Addr: "127.0.0.1:7400"},
	}
}

// Update advances the model one event.
func (m *TUIModel) Update(msg Msg) {
	switch msg := msg.(type) {
	case TabSelectMsg:
		m.tab = msg.Tab
		m.status = fmt.Sprintf("Tab %d active.", msg.Tab+1)
	case FieldMsg:
		m.applyField(msg.Field, msg.Value)
	case RunMsg:
		m.startRun()
	case CmdDoneMsg:
		if msg.ID == 0 || msg.ID != m.pendingID {
			return // no command pending, or stale completion
		}
		m.pendingID = 0
		m.busy = false
		for _, l := range msg.Lines {
			m.log = append(m.log, l)
		}
		if msg.Code == 0 {
			m.status = "done ✓"
		} else {
			m.status = fmt.Sprintf("exit %d (see log)", msg.Code)
		}
		// wizard mode: a finished step command records itself — the user
		// does not press anything extra between a step finishing and Next.
		if m.wizardOn && m.wizard != nil {
			m.wizardRecordFromLines(msg.Code, msg.Lines)
		}
	case LogMsg:
		m.log = append(m.log, msg.Line)
	case WizardToggleMsg:
		if m.wizardOn {
			m.wizardOn = false
			m.status = "wizard closed — tabs as before"
			return
		}
		if m.wizard == nil {
			m.wizard = NewWizard()
		}
		m.wizardOn = true
		m.status = "wizard: " + m.wizard.StepTitle() + " — n next, b back, r run step, w close"
	case WizardNextMsg:
		if !m.wizardOn {
			return
		}
		if err := m.wizard.Next(); err != nil {
			m.status = "wizard: " + err.Error()
			return
		}
		if m.wizard.Finished() {
			m.status = "wizard done — w to close. Report/dry-run/migrate/eval are one key each now."
		} else {
			m.status = "wizard: " + m.wizard.StepTitle() + " — r runs this step"
		}
	case WizardBackMsg:
		if !m.wizardOn {
			return
		}
		m.wizard.Back()
		m.status = "wizard: " + m.wizard.StepTitle()
	case WizardSetInputsMsg:
		if !m.wizardOn {
			return
		}
		if msg.Source != "" {
			_ = m.wizard.SetSource(msg.Source)
		}
		if msg.File != "" {
			_ = m.wizard.SetFile(msg.File)
		}
		if msg.DB != "" {
			_ = m.wizard.SetDB(msg.DB)
		}
		// keep the tabs in sync with what the wizard learned
		m.applyField(FieldSource, m.wizard.Source())
		m.applyField(FieldFile, m.wizard.File())
		m.applyField(FieldDB, m.wizard.DB())
		m.status = "wizard inputs set — n next"
	case WizardRunMsg:
		if !m.wizardOn {
			return
		}
		if m.wizardInputOn {
			m.status = "wizard: finish input first (enter commits, esc cancels)"
			return
		}
		m.wizardRunStep()
	case WizardInputModeMsg:
		if !m.wizardOn {
			return
		}
		if m.wizardInputOn {
			m.wizardInputOn = false
			m.status = "wizard: input cancelled"
			return
		}
		if m.wizard.Step() != StepSource {
			m.status = "wizard: press n until the export step, then i to type the fields"
			return
		}
		m.wizardInputOn = true
		m.wizardInputField = 0
		m.wizardInputBuf = m.wizard.Source()
		m.status = "wizard input: source (langsmith|honeyhive) — enter commits"
	case WizardInputKeyMsg:
		if !m.wizardOn || !m.wizardInputOn {
			return
		}
		m.wizardInputKey(msg.Key)
	case QuitMsg:
		m.finished = true
		if m.serveUp && m.serveStopHook != nil {
			m.serveStopHook()
		}
	case ServeStartMsg:
		if m.serveUp {
			m.status = "serve already running"
			return
		}
		cfg := m.serveCfg
		if m.serveStartHook != nil {
			if err := m.serveStartHook(cfg); err != nil {
				m.status = "serve start failed: " + err.Error()
				return
			}
		}
		m.serveUp = true
		m.status = "serve running at " + cfg.Addr
	case ServeStopMsg:
		if !m.serveUp {
			m.status = "no server running"
			return
		}
		if m.serveStopHook != nil {
			if err := m.serveStopHook(); err != nil {
				m.status = "serve stop failed: " + err.Error()
				return
			}
		}
		m.serveUp = false
		m.status = "serve stopped"
	}
}

func (m *TUIModel) applyField(f Field, v string) {
	switch f {
	case FieldSource:
		m.reportCfg.Source = v
		m.migrateCfg.Source = v
	case FieldFile:
		m.reportCfg.File = v
		m.migrateCfg.File = v
	case FieldOutDir:
		m.reportCfg.OutDir = v
	case FieldDB:
		m.migrateCfg.DB = v
		m.evalCfg.DB = v
		m.serveCfg.DB = v
	case FieldDryRun:
		m.migrateCfg.DryRun = v == "true"
	case FieldTrajectory:
		m.evalCfg.Trajectory = v
	case FieldAssertions:
		m.evalCfg.Assertions = v
	case FieldJudges:
		m.evalCfg.JudgesRun = v
	case FieldJudgeEndpoint:
		m.evalCfg.JudgeEndpoint = v
	case FieldJudgeAPIKey:
		m.evalCfg.JudgeAPIKey = v
	case FieldAddr:
		m.serveCfg.Addr = v
	case FieldSCIM:
		m.serveCfg.SCIM = v == "true"
	case FieldSCIMToken:
		m.serveCfg.SCIMToken = v
	}
}

// startRun builds the command for the active tab and marks busy.
func (m *TUIModel) startRun() {
	if m.busy {
		m.status = "a command is already running"
		return
	}
	var (
		args []string
		err  error
		env  map[string]string
	)
	// The CLI resolves repo-relative resources (judges/manifest.json,
	// registry/bin) from its working directory — the same rule as typing
	// the command yourself. So: inherit the front-end's cwd (Dir stays "").
	// The user who keeps everything in one folder runs the front-end from
	// that folder; a repo checkout runs from the repo root.
	dir := ""
	label := ""
	switch m.tab {
	case TabAnalyze:
		args, err = ReportArgs(m.reportCfg)
		label = "report"
	case TabMigrate:
		args, err = MigrateArgs(m.migrateCfg)
		label = "migrate"
	case TabEvaluate:
		args, err = m.EvalCommandArgs()
		label = "eval"
	case TabServe:
		// serve uses the start/stop lifecycle, not a one-shot run
		m.Update(ServeStartMsg{})
		return
	case TabDonate:
		// donate is informational — nothing to run
		m.status = "donate: addresses shown below (c copies)"
		return
	}
	if err != nil {
		m.status = "invalid config: " + err.Error()
		return
	}
	m.lastCmdID++
	m.pendingID = m.lastCmdID
	m.busy = true
	m.status = "running " + label + "…"
	cmd := Cmd{Label: label, Args: args, Env: env, Dir: dir}
	m.log = append(m.log, fmt.Sprintf("$ proofspan %s", joinArgs(args)))
	if m.runHook != nil {
		m.runHook(cmd)
	}
}

// PendingCmdID returns the ID of the running command (0 if none). The
// runtime runs the command, then delivers CmdDoneMsg{ID: PendingCmdID()}.
// IDs are single-use: a stale completion for an older command is ignored.
func (m *TUIModel) PendingCmdID() int { return m.pendingID }

// ServeCommand returns the argv+env for the configured server (test seam
// mirrors ServeArgs; the TUI binary calls ServeArgs directly).
func (m *TUIModel) ServeCommand() ([]string, map[string]string, error) {
	return ServeArgs(m.serveCfg)
}

// wizardRunStep builds the current wizard page's command — through the
// shared argv builders, never a parallel path — and marks busy like any
// other run. The runtime executes it and delivers CmdDoneMsg.
func (m *TUIModel) wizardRunStep() {
	if m.busy {
		m.status = "a command is already running"
		return
	}
	w := m.wizard
	var (
		args  []string
		label string
		err   error
	)
	switch w.Step() {
	case StepAnalyze:
		args, err = w.ReportCommandArgs()
		label = "report"
	case StepPlan:
		args, err = w.PlanCommandArgs()
		label = "migrate (dry-run)"
	case StepMigrate:
		args, err = w.MigrateCommandArgs()
		label = "migrate"
	case StepEval:
		args, err = w.EvalCommandArgs()
		label = "eval"
	case StepIntro, StepSource, StepDone:
		m.status = "wizard: nothing to run on this page — n to continue"
		return
	default:
		m.status = "wizard: unknown step"
		return
	}
	if err != nil {
		m.status = "wizard: " + err.Error()
		return
	}
	m.lastCmdID++
	m.pendingID = m.lastCmdID
	m.busy = true
	m.status = "wizard: running " + label + "…"
	cmd := Cmd{Label: label, Args: args, Dir: ""}
	m.log = append(m.log, "$ proofspan "+joinArgs(args))
	if m.runHook != nil {
		m.runHook(cmd)
	}
}

// wizardInputKey processes one keystroke in wizard input mode: printable
// characters edit the buffer, backspace deletes, enter commits the field
// and moves to the next (source → file → db → exit input mode).
func (m *TUIModel) wizardInputKey(key string) {
	switch key {
	case "enter":
		commitErr := func(err error) {
			m.status = "wizard input: " + err.Error() + " — edit and press enter again"
			// keep the buffer as typed (the user sees what failed); the
			// field is NOT advanced on error.
		}
		switch m.wizardInputField {
		case 0:
			if err := m.wizard.SetSource(m.wizardInputBuf); err != nil {
				commitErr(err)
				return
			}
		case 1:
			if err := m.wizard.SetFile(m.wizardInputBuf); err != nil {
				commitErr(err)
				return
			}
		case 2:
			if err := m.wizard.SetDB(m.wizardInputBuf); err != nil {
				m.status = "wizard input: " + err.Error()
				return
			}
			// last field: commit into the tabs and leave input mode
			m.applyField(FieldSource, m.wizard.Source())
			m.applyField(FieldFile, m.wizard.File())
			m.applyField(FieldDB, m.wizard.DB())
			m.wizardInputOn = false
			m.status = "wizard inputs set — r runs this step, n next"
			return
		}
		m.wizardInputField++
		m.wizardInputBuf = m.wizardInputFieldValue(m.wizardInputField)
		m.status = "wizard input: " + m.wizardInputFieldName(m.wizardInputField) + " — enter commits"
	case "backspace":
		if len(m.wizardInputBuf) > 0 {
			m.wizardInputBuf = m.wizardInputBuf[:len(m.wizardInputBuf)-1]
		}
	case "esc":
		m.wizardInputOn = false
		m.status = "wizard: input cancelled"
	default:
		if len(key) == 1 {
			m.wizardInputBuf += key
		}
	}
}

func (m *TUIModel) wizardInputFieldValue(field int) string {
	switch field {
	case 0:
		return m.wizard.Source()
	case 1:
		return m.wizard.File()
	case 2:
		return m.wizard.DB()
	}
	return ""
}

func (m *TUIModel) wizardInputFieldName(field int) string {
	switch field {
	case 0:
		return "source (langsmith|honeyhive)"
	case 1:
		return "export file path"
	case 2:
		return "database path"
	}
	return ""
}

// WizardInputActive exposes the inline-input state for the renderer.
func (m *TUIModel) WizardInputActive() (bool, int, string) {
	return m.wizardInputOn, m.wizardInputField, m.wizardInputBuf
}

// wizardRecordFromLines marks the current wizard step as run, deriving the
// one-line summary from the command's real output (never invented text).
// The step's human summary lines: report → report JSON on stdout is not
// streamed, so we summarize from the status line the runner appended.
func (m *TUIModel) wizardRecordFromLines(code int, lines []string) {
	w := m.wizard
	if w == nil {
		return
	}
	pick := func(prefixes ...string) string {
		for _, l := range lines {
			for _, p := range prefixes {
				if strings.HasPrefix(l, p) {
					return l
				}
			}
		}
		return fmt.Sprintf("exit %d", code)
	}
	// joinedJSON reassembles the machine-readable channel: report and the
	// dry-run plan print pure JSON on stdout (nothing on stderr), so the
	// summary must be parsed from the streamed lines, not prefixed text.
	joinedJSON := func() string {
		start, end := -1, -1
		for i, l := range lines {
			if strings.TrimSpace(l) == "{" && start == -1 {
				start = i
			}
			if strings.TrimSpace(l) == "}" {
				end = i
			}
		}
		if start == -1 || end == -1 || end < start {
			return ""
		}
		return strings.Join(lines[start:end+1], "\n")
	}
	switch w.Step() {
	case StepAnalyze:
		rep := struct {
			LinesParsed  int `json:"lines_parsed"`
			LinesTotal   int `json:"lines_total"`
			Trajectories int `json:"trajectories"`
		}{}
		if err := json.Unmarshal([]byte(joinedJSON()), &rep); err == nil && rep.LinesTotal > 0 {
			w.RecordAnalyzed(fmt.Sprintf("report: %d/%d lines parsed, %d trajectories",
				rep.LinesParsed, rep.LinesTotal, rep.Trajectories))
		} else {
			w.RecordAnalyzed(pick("report:"))
		}
	case StepPlan:
		plan := struct {
			Trajectories  int `json:"trajectories"`
			SpansPlanned  int `json:"spans_planned"`
			DroppedFields []struct {
				Field string `json:"field"`
			} `json:"dropped_fields"`
		}{}
		if err := json.Unmarshal([]byte(joinedJSON()), &plan); err == nil && plan.SpansPlanned > 0 {
			w.RecordPlanned(fmt.Sprintf("plan: %d trajectories / %d spans / %d dropped fields",
				plan.Trajectories, plan.SpansPlanned, len(plan.DroppedFields)))
		} else {
			w.RecordPlanned(pick("plan:", "migrate:"))
		}
	case StepMigrate:
		w.RecordMigrated(pick("migrated "))
	case StepEval:
		w.RecordEval(pick("eval:"))
	}
}

// WizardState exposes the wizard for the renderer (nil when off).
func (m *TUIModel) WizardState() (*Wizard, bool) { return m.wizard, m.wizardOn }

// --- read accessors for the renderer and tests ---

func (m *TUIModel) ActiveTab() Tab            { return m.tab }
func (m *TUIModel) Busy() bool                { return m.busy }
func (m *TUIModel) Finished() bool            { return m.finished }
func (m *TUIModel) Status() string            { return m.status }
func (m *TUIModel) LogLines() []string        { return m.log }
func (m *TUIModel) ServeRunning() bool        { return m.serveUp }
func (m *TUIModel) MigrateCfg() MigrateConfig { return m.migrateCfg }
func (m *TUIModel) EvalCfg() EvalConfig       { return m.evalCfg }
func (m *TUIModel) ReportCfg() ReportConfig   { return m.reportCfg }
func (m *TUIModel) ServeCfg() ServeConfig     { return m.serveCfg }

// EvalCommandArgs returns the full eval argv, pinning --judges/--registry
// when a repo checkout is known — making the command independent of cwd.
func (m *TUIModel) EvalCommandArgs() ([]string, error) {
	args, err := EvalArgs(m.evalCfg)
	if err != nil {
		return nil, err
	}
	if m.resourcesDir != "" {
		args = append(args,
			"--judges="+filepath.Join(m.resourcesDir, "judges", "manifest.json"),
			"--registry="+filepath.Join(m.resourcesDir, "registry", "bin"),
		)
	}
	return args, nil
}

// SetResourcesDir pins (or clears, with "") the repo checkout root used
// for --judges/--registry. DetectResourcesDir fills it automatically.
func (m *TUIModel) SetResourcesDir(dir string) { m.resourcesDir = dir }

// DetectResourcesDir walks up from start looking for a directory holding
// both judges/manifest.json and registry/ — the repo checkout. Returns ""
// when none is found.
func DetectResourcesDir(start string) string {
	dir := start
	// A relative start like "." dead-ends immediately: filepath.Dir(".") == "."
	// makes the walk-up see parent == dir and stop. Absolutize so the walk
	// can actually ascend to the repo checkout.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for i := 0; i < 12; i++ {
		if fileExists(filepath.Join(dir, "judges", "manifest.json")) &&
			fileExists(filepath.Join(dir, "registry")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (m *TUIModel) applyLog(line string) { m.log = append(m.log, line) }

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
