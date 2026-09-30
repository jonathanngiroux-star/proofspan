// Command proofspan-tui is a terminal front-end over the proofspan CLI —
// the tmux/SSH-native sibling of the GUI. Same model, same argv builders,
// same commands. Bubble Tea drives the pure TUIModel in internal/app.
//
// Build: cd ui/cmd/proofspan-tui && go build -o proofspan-tui .
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"proofspan/ui/internal/app"
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
		case "enter", "r":
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
		code, err := r.Run(args, env, func(line string) {})
		if err != nil {
			return app.LogMsg{Line: "error: " + err.Error()}
		}
		return app.CmdDoneMsg{ID: id, Code: code}
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
	b.WriteString(w.pane())
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

	b.WriteString("\n" + helpStyle.Render("keys: 1-4 tabs · enter/r run · s start/stop serve · q quit"))
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
	}
	return ""
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
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
