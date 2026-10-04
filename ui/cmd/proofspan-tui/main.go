// Command proofspan-tui is a terminal front-end over the proofspan CLI —
// the tmux/SSH-native sibling of the GUI. Same model, same argv builders,
// same commands. Bubble Tea drives the pure TUIModel in internal/app.
//
// Build: cd ui/cmd/proofspan-tui && go build -o proofspan-tui .
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonathanngiroux-star/proofspan/ui/internal/app"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	tabStyle   = lipgloss.NewStyle().Padding(0, 1)
	activeTab  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("62")).Padding(0, 1)
	statusOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	statusBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	logStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

// wrapper adapts the pure model to bubbletea's Model interface.
type wrapper struct {
	m    *app.TUIModel
	proc *app.ServeProcess
}

func (w wrapper) Init() tea.Cmd { return nil }

func (w wrapper) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.KeyMsg:
		// wizard input mode eats every key first: the user is typing a
		// path; tab-switch keys must not fire mid-edit.
		if on, _, _ := w.m.WizardInputActive(); on {
			// In input mode every key goes to the field — including 'q'
			// (paths contain it: proofspan.sqlite, /sql/...). Only ctrl+c
			// exits mid-edit, as in any editor.
			if t.String() == "ctrl+c" {
				w.m.Update(app.WizardInputKeyMsg{Key: "esc"})
			} else {
				w.m.Update(app.WizardInputKeyMsg{Key: t.String()})
			}
			return w, nil
		}
		switch t.String() {
		case "q", "ctrl+c":
			if w.m.ServeRunning() && w.proc != nil {
				w.proc.Stop()
			}
			w.m.Update(app.QuitMsg{})
			return w, tea.Quit
		case "1":
			w.m.Update(app.TabSelectMsg{Tab: app.TabAnalyze})
		case "2":
			w.m.Update(app.TabSelectMsg{Tab: app.TabMigrate})
		case "3":
			w.m.Update(app.TabSelectMsg{Tab: app.TabEvaluate})
		case "4":
			w.m.Update(app.TabSelectMsg{Tab: app.TabServe})
		case "5":
			w.m.Update(app.TabSelectMsg{Tab: app.TabDonate})
		case "c", "C":
			// copy donate address from the donate tab (c = ETH, shift+C = BTC;
			// the pane shows both keys)
			if w.m.ActiveTab() == app.TabDonate {
				text := app.DonateETH
				if t.String() == "C" {
					text = app.DonateBTC
				}
				return w, copyToClipboardCmd(text)
			}
		case "w":
			// wizard: the guided walkthrough (report → dry-run → migrate → eval)
			if _, on := w.m.WizardState(); on {
				w.m.Update(app.WizardToggleMsg{}) // close
			} else {
				// prefill from the tabs so the wizard starts where the user is
				w.m.Update(app.WizardToggleMsg{})
				mig := w.m.MigrateCfg()
				w.m.Update(app.WizardSetInputsMsg{Source: mig.Source, File: mig.File, DB: mig.DB})
			}
		case "i":
			// wizard inline input: type the export path etc. right here
			if _, on := w.m.WizardState(); on {
				w.m.Update(app.WizardInputModeMsg{})
			}
		case "n":
			if _, on := w.m.WizardState(); on {
				w.m.Update(app.WizardNextMsg{})
			}
		case "b":
			if _, on := w.m.WizardState(); on {
				w.m.Update(app.WizardBackMsg{})
			}
		case "enter", "r":
			if _, on := w.m.WizardState(); on {
				return w, w.startWizardCommand()
			}
			return w, w.startCommand()
		case "s":
			if w.m.ActiveTab() == app.TabServe {
				if w.m.ServeRunning() {
					if w.proc != nil {
						w.proc.Stop()
					}
					w.m.Update(app.ServeStopMsg{})
				} else {
					return w, w.startServe()
				}
			}
		}
	case tea.WindowSizeMsg:
		// re-render at new size; nothing stateful to store
		return w, nil
	case app.CmdDoneMsg:
		w.m.Update(msg)
	case app.LogMsg:
		w.m.Update(msg)
	}
	return w, nil
}

// startCommand builds the active tab's command and runs it, streaming
// each output line back as a bubbletea message.
func (w wrapper) startCommand() tea.Cmd {
	tab := w.m.ActiveTab()
	var (
		args []string
		err  error
		env  map[string]string
	)
	switch tab {
	case app.TabAnalyze:
		// the model validated config on RunMsg; here we re-derive argv
		args, err = app.ReportArgs(w.m.ReportCfg())
	case app.TabMigrate:
		args, err = app.MigrateArgs(w.m.MigrateCfg())
	case app.TabEvaluate:
		args, err = w.m.EvalCommandArgs()
	case app.TabServe:
		return w.startServe()
	case app.TabDonate:
		// nothing to run on the donate tab
		return nil
	}
	if err != nil {
		w.m.Update(app.LogMsg{Line: "config error: " + err.Error()})
		return nil
	}
	id := w.m.PendingCmdID()
	w.m.Update(app.RunMsg{})
	if w.m.PendingCmdID() == 0 {
		return nil // busy guard tripped or invalid config; status explains
	}
	id = w.m.PendingCmdID()
	r := &app.Runner{} // Dir="" → inherit cwd, matching CLI-at-a-terminal semantics
	return tea.Batch(func() tea.Msg {
		var lines []string
		code, err := r.Run(args, env, func(line string) { lines = append(lines, line) })
		if err != nil {
			return app.LogMsg{Line: "error: " + err.Error()}
		}
		return app.CmdDoneMsg{ID: id, Code: code, Lines: lines}
	})
}

// startWizardCommand runs the current wizard step's command — the same
// Runner path as a normal tab run.
func (w wrapper) startWizardCommand() tea.Cmd {
	w.m.Update(app.WizardRunMsg{})
	if !w.m.Busy() || w.m.PendingCmdID() == 0 {
		return nil // refused: invalid inputs or nothing to run on this page
	}
	id := w.m.PendingCmdID()
	// capture argv now — the model doesn't expose the built Cmd
	wiz, _ := w.m.WizardState()
	var (
		args []string
		err  error
	)
	switch wiz.Step() {
	case app.StepAnalyze:
		args, err = wiz.ReportCommandArgs()
	case app.StepPlan:
		args, err = wiz.PlanCommandArgs()
	case app.StepMigrate:
		args, err = wiz.MigrateCommandArgs()
	case app.StepEval:
		args, err = wiz.EvalCommandArgs()
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	r := &app.Runner{}
	return tea.Batch(func() tea.Msg {
		var lines []string
		code, err := r.Run(args, nil, func(line string) { lines = append(lines, line) })
		if err != nil {
			return app.LogMsg{Line: "error: " + err.Error()}
		}
		return app.CmdDoneMsg{ID: id, Code: code, Lines: lines}
	})
}

// startServe launches the server and reports status.
func (w wrapper) startServe() tea.Cmd {
	args, env, err := w.m.ServeCommand()
	if err != nil {
		w.m.Update(app.LogMsg{Line: "serve config error: " + err.Error()})
		return nil
	}
	dir := app.DBDir(w.m.ServeCfg().DB)
	proc, err := app.StartServe(args, env, dir, func(line string) {})
	if err != nil {
		w.m.Update(app.LogMsg{Line: "serve start failed: " + err.Error()})
		return nil
	}
	w.proc = proc
	w.m.Update(app.ServeStartMsg{})
	w.m.Update(app.LogMsg{Line: "serve up at " + w.m.ServeCfg().Addr})
	return nil
}

func (w wrapper) View() string {
	m := w.m
	var b strings.Builder
	b.WriteString(titleStyle.Render("proofspan") + helpStyle.Render("  — agent eval harness (tui)") + "\n\n")

	// tab bar
	tabs := []struct {
		label string
		tab   app.Tab
	}{
		{"1 Analyze", app.TabAnalyze},
		{"2 Migrate", app.TabMigrate},
		{"3 Evaluate", app.TabEvaluate},
		{"4 Serve", app.TabServe},
		{"5 Donate", app.TabDonate},
	}
	for i, t := range tabs {
		style := tabStyle
		if m.ActiveTab() == t.tab {
			style = activeTab
		}
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(style.Render(t.label))
	}
	b.WriteString("\n\n")

	// active pane fields (rendered read-only; the user edits via prompts
	// in v0.1 — tab+enter run covers the 90% path)
	if wiz, on := m.WizardState(); on {
		b.WriteString(w.wizardPane(wiz))
	} else {
		b.WriteString(w.pane())
	}
	b.WriteString("\n\n")

	// status
	st := m.Status()
	if strings.Contains(st, "exit") && !strings.Contains(st, "exit 0") {
		b.WriteString(statusBad.Render(st))
	} else {
		b.WriteString(statusOK.Render(st))
	}
	b.WriteString("\n\n")

	// log tail (last 12 lines)
	lines := m.LogLines()
	start := 0
	if len(lines) > 12 {
		start = len(lines) - 12
	}
	for _, l := range lines[start:] {
		b.WriteString(logStyle.Render(l) + "\n")
	}

	help := "keys: 1-5 tabs · enter/r run · s serve · w wizard · q quit"
	if m.ActiveTab() == app.TabDonate {
		help = "donate: c copy ETH · C copy BTC · 1-5 tabs · q quit"
	}
	if _, on := m.WizardState(); on {
		help = "wizard: n next · b back · i input fields · enter/r run step · w close · q quit"
	}
	b.WriteString("\n" + helpStyle.Render(help))
	return b.String()
}

// pane renders the active tab's current config.
func (w wrapper) pane() string {
	m := w.m
	switch m.ActiveTab() {
	case app.TabAnalyze:
		c := m.ReportCfg()
		return fmt.Sprintf("source: %s\nfile:   %s\nout:    %s", c.Source, orDash(c.File), orDash(c.OutDir))
	case app.TabMigrate:
		c := m.MigrateCfg()
		return fmt.Sprintf("source: %s\nfile:   %s\ndb:     %s\ndry-run: %t", c.Source, orDash(c.File), c.DB, c.DryRun)
	case app.TabEvaluate:
		c := m.EvalCfg()
		return fmt.Sprintf("db:      %s\ntraj:    %s\nassert:  %s\njudges:  %s\nendpoint:%s",
			c.DB, orDash(c.Trajectory), orDash(c.Assertions), orDash(c.JudgesRun), orDash(c.JudgeEndpoint))
	case app.TabServe:
		c := m.ServeCfg()
		state := "down"
		if m.ServeRunning() {
			state = "UP"
		}
		return fmt.Sprintf("db:    %s\naddr:  %s\nscim:  %t\nstate: %s", c.DB, c.Addr, c.SCIM, state)
	case app.TabDonate:
		addrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("251"))
		return fmt.Sprintf("Proofspan is donation-supported, never gated.\nIf it killed your eval-spreadsheet week:\n\nEthereum / USDC (ERC-20)\n%s\n[c copy ETH]\n\nBitcoin\n%s\n[C copy BTC]",
			addrStyle.Render(app.DonateETH), addrStyle.Render(app.DonateBTC))
	}
	return ""
}

// wizardPane renders the active wizard page (title, body, inputs, summary).
func (w wrapper) wizardPane(wiz *app.Wizard) string {
	var b strings.Builder
	b.WriteString(wiz.StepTitle() + "\n")
	// step indicator line: explicit, testable, and readable mid-flow
	b.WriteString(fmt.Sprintf("(wizard step %d/7 — n next, b back, r run, i input, w close)\n", int(wiz.Step())+1))
	b.WriteString(strings.Repeat("─", 46) + "\n")
	body := wiz.StepBody()
	// wrap the guidance at ~62 cols so the pane stays readable
	for _, para := range strings.Split(body, "\n") {
		for len(para) > 62 {
			cut := strings.LastIndex(para[:62], " ")
			if cut <= 0 {
				cut = 62
			}
			b.WriteString(para[:cut] + "\n")
			para = para[cut:]
		}
		b.WriteString(para + "\n")
	}
	// inline input line (when active)
	if on, field, buf := w.m.WizardInputActive(); on {
		b.WriteString("\n  input > " + buf + "\n")
		b.WriteString("  (" + w.wizardInputFieldNameRuntime(field) + " — enter commits, esc cancels)\n")
	}
	a, p, mg, e := wiz.Summaries()
	switch wiz.Step() {
	case app.StepAnalyze:
		if a != "" {
			b.WriteString("\n  " + logStyle.Render(a))
		}
	case app.StepPlan:
		if p != "" {
			b.WriteString("\n  " + logStyle.Render(p))
		}
	case app.StepMigrate:
		if mg != "" {
			b.WriteString("\n  " + logStyle.Render(mg))
		}
	case app.StepEval:
		if e != "" {
			b.WriteString("\n  " + logStyle.Render(e))
		}
	}
	return b.String()
}

// wizardInputFieldNameRuntime maps the field index to its label for the
// renderer (mirrors the model's name table).
func (w wrapper) wizardInputFieldNameRuntime(field int) string {
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

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// copyToClipboardCmd copies text to the terminal's clipboard using OSC 52 —
// the escape sequence every modern terminal (kitty, iTerm2, WezTerm, Windows
// Terminal, gnome-terminal with OSC 52 support) understands, including over
// SSH. It runs as a tea.Cmd so the write happens off the render loop, and
// lands a status message the View renders.
func copyToClipboardCmd(text string) tea.Cmd {
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	// OSC 52: ESC ] 52 ; c ; <base64> BEL  — 'c' is the clipboard selection.
	seq := "\x1b]52;c;" + enc + "\x07"
	which := "ETH"
	if strings.HasPrefix(text, "bc1") {
		which = "BTC"
	}
	return func() tea.Msg {
		// Write to the TTY bubbletea handed us — tea's output goes through
		// the same writer, so the sequence reaches the terminal even over
		// SSH with the pty attached.
		if f, ok := ttyWriter(); ok {
			_, _ = f.WriteString(seq)
		}
		return app.LogMsg{Line: "donate: " + which + " address copied to clipboard"}
	}
}

// ttyWriter opens the controlling terminal for the OSC 52 write.
func ttyWriter() (*os.File, bool) {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, false
	}
	return f, true
}

func main() {
	m := app.NewTUIModel()
	if cwd, err := os.Getwd(); err == nil {
		m.SetResourcesDir(app.DetectResourcesDir(cwd))
	}
	p := tea.NewProgram(wrapper{m: m}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
