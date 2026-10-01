package app

import (
	"fmt"
	"os"
	"path/filepath"
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

const (
	TabAnalyze Tab = iota
	TabMigrate
	TabEvaluate
	TabServe
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
	case LogMsg:
		m.log = append(m.log, msg.Line)
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
