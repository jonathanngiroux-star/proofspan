// Command proofspan-gui is a thin Fyne front-end over the proofspan CLI.
//
// Scope (AGENTS.md): the GUI drives the existing commands — report, migrate,
// eval, serve — with the same flags. No product logic lives here; the CLI
// remains the product. Lives in gui/ as a separate Go module so Fyne's
// dependency tree (and CGO) never touches the core binary's supply chain.
//
// Build:  cd gui && go build -o proofspan-gui .
// Env:    PROOFSPAN_BIN overrides the binary path (default: "proofspan" on PATH)
package main

import (
	"fmt"
	"strings"
	"sync"

	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"proofspan/gui/internal/app"
)

func main() {
	a := fyneapp.NewWithID("dev.proofspan.gui")
	w := a.NewWindow("Proofspan")
	w.Resize(fyne.NewSize(900, 680))

	ui := newUI(w)
	w.SetContent(ui.root())
	w.SetCloseIntercept(func() {
		ui.stopServeSilently()
		w.Close()
	})
	w.ShowAndRun()
}

// ui holds every widget the four tabs need plus the shared log/status area.
type ui struct {
	win fyne.Window

	// report tab
	reportSource *widget.Select
	reportFile   *widget.Entry
	reportOut    *widget.Entry

	// migrate tab
	migrateSource *widget.Select
	migrateFile   *widget.Entry
	migrateDB     *widget.Entry
	migrateDryRun *widget.Check

	// eval tab
	evalDB         *widget.Entry
	evalTrajectory *widget.Entry
	evalAssertions *widget.Entry
	evalJudges     *widget.Entry
	evalEndpoint   *widget.Entry
	evalAPIKey     *widget.Entry

	// serve tab
	serveDB    *widget.Entry
	serveAddr  *widget.Entry
	serveSCIM  *widget.Check
	serveToken *widget.Entry

	// shared chrome
	log   *widget.Entry
	logMu sync.Mutex
	stat  *widget.Label

	running   bool
	activeTab string
	procMu    sync.Mutex
	serveProc *app.ServeProcess
}

func newUI(w fyne.Window) *ui {
	u := &ui{win: w}
	u.buildWidgets()
	return u
}

func (u *ui) buildWidgets() {
	// --- report ---
	u.reportSource = widget.NewSelect([]string{"langsmith", "honeyhive"}, func(string) {})
	u.reportSource.SetSelected("langsmith")
	u.reportFile = widget.NewEntry()
	u.reportFile.PlaceHolder = "/path/to/traces.jsonl"
	u.reportOut = widget.NewEntry()
	u.reportOut.PlaceHolder = "optional: output dir for the markdown report"

	// --- migrate ---
	u.migrateSource = widget.NewSelect([]string{"langsmith", "honeyhive"}, func(string) {})
	u.migrateSource.SetSelected("langsmith")
	u.migrateFile = widget.NewEntry()
	u.migrateFile.PlaceHolder = "/path/to/traces.jsonl"
	u.migrateDB = widget.NewEntry()
	u.migrateDB.SetText("proofspan.sqlite")
	u.migrateDryRun = widget.NewCheck("Dry-run (plan only, no writes)", nil)

	// --- eval ---
	u.evalDB = widget.NewEntry()
	u.evalDB.SetText("proofspan.sqlite")
	u.evalTrajectory = widget.NewEntry()
	u.evalTrajectory.PlaceHolder = "optional: single trajectory ID (default: all)"
	u.evalAssertions = widget.NewEntry()
	u.evalAssertions.PlaceHolder = "optional: id@version pins (default: span-correlation@1.0.0)"
	u.evalJudges = widget.NewEntry()
	u.evalJudges.PlaceHolder = "optional: judge IDs, comma-separated (e.g. factual-consistency)"
	u.evalEndpoint = widget.NewEntry()
	u.evalEndpoint.PlaceHolder = "optional: judge endpoint, e.g. http://localhost:8000/v1"
	u.evalAPIKey = widget.NewMultiLineEntry()
	u.evalAPIKey.PlaceHolder = "optional: judge API key (the manifest's env var is safer)"
	u.evalAPIKey.Password = true
	u.evalAPIKey.SetMinRowsVisible(1)

	// --- serve ---
	u.serveDB = widget.NewEntry()
	u.serveDB.SetText("proofspan.sqlite")
	u.serveAddr = widget.NewEntry()
	u.serveAddr.SetText("127.0.0.1:7400")
	u.serveSCIM = widget.NewCheck("Mount SCIM provider", nil)
	u.serveToken = widget.NewEntry()
	u.serveToken.PlaceHolder = "PROOFSPAN_SCIM_TOKEN (empty = dev mode, localhost only)"
	u.serveToken.Password = true

	// --- shared ---
	u.log = widget.NewMultiLineEntry()
	u.log.Disable() // read-only log; Append() still works on disabled entries
	u.stat = widget.NewLabel("Ready.")
}

func (u *ui) root() fyne.CanvasObject {
	rp := widget.NewButton("Browse…", func() { u.pickJSONL(u.reportFile) })
	mg := widget.NewButton("Browse…", func() { u.pickJSONL(u.migrateFile) })

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Analyze", theme.FolderOpenIcon(),
			container.NewVScroll(widget.NewForm(
				widget.NewFormItem("Source", u.reportSource),
				widget.NewFormItem("Export file", container.NewBorder(nil, nil, nil, rp, u.reportFile)),
				widget.NewFormItem("Report dir (optional)", u.reportOut),
			))),
		container.NewTabItemWithIcon("Migrate", theme.StorageIcon(),
			container.NewVScroll(widget.NewForm(
				widget.NewFormItem("Source", u.migrateSource),
				widget.NewFormItem("Export file", container.NewBorder(nil, nil, nil, mg, u.migrateFile)),
				widget.NewFormItem("Database", u.migrateDB),
				widget.NewFormItem("", u.migrateDryRun),
			))),
		container.NewTabItemWithIcon("Evaluate", theme.MediaPlayIcon(),
			container.NewVScroll(widget.NewForm(
				widget.NewFormItem("Database", u.evalDB),
				widget.NewFormItem("Trajectory (optional)", u.evalTrajectory),
				widget.NewFormItem("Assertions (optional)", u.evalAssertions),
				widget.NewFormItem("Judges (optional)", u.evalJudges),
				widget.NewFormItem("Judge endpoint (optional)", u.evalEndpoint),
				widget.NewFormItem("Judge API key (optional)", u.evalAPIKey),
			))),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	switcher := container.NewVBox()
	tabs.OnChanged = func(t *container.TabItem) {
		u.activeTab = t.Text
		switch t.Text {
		case "Analyze":
			switcher.Objects = []fyne.CanvasObject{u.btn("Analyze export", u.runReport)}
		case "Migrate":
			switcher.Objects = []fyne.CanvasObject{u.btn("Migrate", u.runMigrate)}
		case "Evaluate":
			switcher.Objects = []fyne.CanvasObject{u.btn("Run evaluation", u.runEval)}
		case "Serve":
			switcher.Objects = []fyne.CanvasObject{u.serveControls()}
		}
		switcher.Refresh()
	}
	tabs.OnChanged(tabs.Items[0])

	logHeader := container.NewBorder(nil, nil, nil, widget.NewButton("Clear log", func() { u.log.SetText("") }), widget.NewLabel("Output"))

	body := container.NewBorder(
		nil,
		container.NewVBox(u.stat),
		nil,
		nil,
		container.NewVSplit(
			container.NewBorder(container.NewVBox(tabs, switcher), nil, nil, nil, nil),
			container.NewBorder(logHeader, nil, nil, nil, u.log),
		),
	)
	return body
}

func (u *ui) btn(label string, fn func()) *widget.Button {
	b := widget.NewButton(label, fn)
	b.Importance = widget.HighImportance
	return b
}

func (u *ui) serveControls() fyne.CanvasObject {
	start := u.btn("Start server", u.startServe)
	stop := widget.NewButton("Stop server", u.stopServe)
	serveForm := widget.NewForm(
		widget.NewFormItem("Database", u.serveDB),
		widget.NewFormItem("Address", u.serveAddr),
		widget.NewFormItem("", u.serveSCIM),
		widget.NewFormItem("SCIM token (optional)", u.serveToken),
	)
	return container.NewVBox(serveForm, container.NewHBox(start, stop))
}

func (u *ui) pickJSONL(target *widget.Entry) {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			return
		}
		defer rc.Close()
		target.SetText(rc.URI().Path())
	}, u.win)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".jsonl", ".json"}))
	fd.Show()
}

// runCmd executes one command in the background, streaming output lines
// into the log area. Guards against concurrent runs. Workdir = the DB
// file's directory so the CLI's relative defaults (judges/manifest.json,
// registry/bin) resolve.
func (u *ui) runCmd(label string, args []string, extraEnv map[string]string, onDone func(code int)) {
	if u.running {
		dialog.ShowInformation("Busy", "A command is already running; wait for it to finish.", u.win)
		return
	}
	u.running = true
	u.setStatus("Running: " + label + "…")
	u.appendLog(fmt.Sprintf("$ proofspan %s", strings.Join(args, " ")))

	r := &app.Runner{Dir: dbDir(u.currentDB())}
	go func() {
		code, err := r.Run(args, extraEnv, func(line string) { u.appendLog(line) })
		u.running = false
		if err != nil {
			u.appendLog("error: " + err.Error())
			u.setStatus(label + ": failed to start")
			dialog.ShowError(err, u.win)
			return
		}
		if code == 0 {
			u.setStatus(label + ": done ✓")
		} else {
			u.setStatus(fmt.Sprintf("%s: exit %d (see output)", label, code))
		}
		u.appendLog(fmt.Sprintf("[exit %d]", code))
		if onDone != nil {
			onDone(code)
		}
	}()
}

// currentDB returns the db path of whichever tab the user is on, so the
// command's working directory follows it.
func (u *ui) currentDB() string {
	switch u.activeTab {
	case "Migrate":
		return u.migrateDB.Text
	case "Serve":
		return u.serveDB.Text
	case "Evaluate":
		return u.evalDB.Text
	default:
		return u.evalDB.Text
	}
}

// dbDir extracts the directory part of a db path ("." for bare names).
func dbDir(dbPath string) string {
	if i := strings.LastIndexByte(dbPath, '/'); i > 0 {
		return dbPath[:i]
	}
	return "."
}

func (u *ui) runReport() {
	args, err := app.ReportArgs(app.ReportConfig{
		Source: u.reportSource.Selected,
		File:   u.reportFile.Text,
		OutDir: u.reportOut.Text,
	})
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	u.runCmd("Analyze", args, nil, nil)
}

func (u *ui) runMigrate() {
	args, err := app.MigrateArgs(app.MigrateConfig{
		Source: u.migrateSource.Selected,
		File:   u.migrateFile.Text,
		DB:     u.migrateDB.Text,
		DryRun: u.migrateDryRun.Checked,
	})
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	u.runCmd("Migrate", args, nil, nil)
}

func (u *ui) runEval() {
	args, err := app.EvalArgs(app.EvalConfig{
		DB:            u.evalDB.Text,
		Trajectory:    u.evalTrajectory.Text,
		Assertions:    u.evalAssertions.Text,
		JudgesRun:     u.evalJudges.Text,
		JudgeEndpoint: u.evalEndpoint.Text,
		JudgeAPIKey:   u.evalAPIKey.Text,
	})
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	u.runCmd("Evaluate", args, nil, nil)
}

func (u *ui) startServe() {
	u.procMu.Lock()
	if u.serveProc != nil && u.serveProc.Running() {
		u.procMu.Unlock()
		dialog.ShowInformation("Already running", "The server is already up.", u.win)
		return
	}
	u.procMu.Unlock()
	args, env, err := app.ServeArgs(app.ServeConfig{
		DB:        u.serveDB.Text,
		Addr:      u.serveAddr.Text,
		SCIM:      u.serveSCIM.Checked,
		SCIMToken: u.serveToken.Text,
	})
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	tokenNote := ""
	if u.serveToken.Text != "" {
		tokenNote = "PROOFSPAN_SCIM_TOKEN=*** "
	}
	u.appendLog(fmt.Sprintf("$ %sproofspan %s", tokenNote, strings.Join(args, " ")))
	proc, err := app.StartServe(args, env, dbDir(u.serveDB.Text), func(line string) { u.appendLog(line) })
	if err != nil {
		dialog.ShowError(err, u.win)
		return
	}
	u.procMu.Lock()
	u.serveProc = proc
	u.procMu.Unlock()
	u.setStatus("Serve: running at " + u.serveAddr.Text)
}

func (u *ui) stopServe() {
	if !u.stopServeSilently() {
		dialog.ShowInformation("Not running", "No server to stop.", u.win)
	}
}

func (u *ui) stopServeSilently() bool {
	u.procMu.Lock()
	defer u.procMu.Unlock()
	if u.serveProc == nil || !u.serveProc.Running() {
		return false
	}
	if err := u.serveProc.Stop(); err != nil {
		u.appendLog("serve stop error: " + err.Error())
	}
	u.serveProc = nil
	u.setStatus("Serve: stopped")
	u.appendLog("[serve stopped]")
	return true
}

func (u *ui) appendLog(line string) {
	u.logMu.Lock()
	defer u.logMu.Unlock()
	u.log.Append(line + "\n")
}

func (u *ui) setStatus(s string) { u.stat.Text = s; u.stat.Refresh() }
