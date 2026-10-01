// Package app is the Proofspan GUI's non-UI core: it builds the exact CLI
// argv for each command and runs the binary. The GUI is a thin driver over
// the CLI — no product logic lives here, ever.
package app

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ResolveBinary picks the proofspan binary: $PROOFSPAN_BIN override, else
// "proofspan" resolved via PATH at exec time.
func ResolveBinary() string {
	if v := os.Getenv("PROOFSPAN_BIN"); v != "" {
		return v
	}
	return "proofspan"
}

// ReportConfig drives `proofspan report`.
type ReportConfig struct {
	Source string // langsmith | honeyhive
	File   string
	OutDir string // optional --out
}

func ReportArgs(c ReportConfig) ([]string, error) {
	if c.Source != "langsmith" && c.Source != "honeyhive" {
		return nil, fmt.Errorf("unknown source %q (langsmith|honeyhive)", c.Source)
	}
	if c.File == "" {
		return nil, fmt.Errorf("input file required")
	}
	args := []string{"report", "--from=" + c.Source}
	if c.OutDir != "" {
		args = append(args, "--out="+c.OutDir)
	}
	return append(args, c.File), nil
}

// MigrateConfig drives `proofspan migrate`.
type MigrateConfig struct {
	Source string
	File   string
	DB     string
	DryRun bool
}

func MigrateArgs(c MigrateConfig) ([]string, error) {
	if c.Source != "langsmith" && c.Source != "honeyhive" {
		return nil, fmt.Errorf("unknown source %q (langsmith|honeyhive)", c.Source)
	}
	if c.File == "" {
		return nil, fmt.Errorf("input file required")
	}
	if c.DB == "" {
		return nil, fmt.Errorf("database path required")
	}
	args := []string{"migrate", "--from=" + c.Source, "--db=" + c.DB}
	if c.DryRun {
		args = append(args, "--dry-run")
	}
	return append(args, c.File), nil
}

// EvalConfig drives `proofspan eval`.
type EvalConfig struct {
	DB            string
	Trajectory    string // optional --trajectory
	Assertions    string // optional --assertions
	JudgesRun     string // optional --judges-run
	JudgeEndpoint string // optional --judge-endpoint
	JudgeAPIKey   string // optional --judge-api-key (env var is safer)
}

func EvalArgs(c EvalConfig) ([]string, error) {
	if c.DB == "" {
		return nil, fmt.Errorf("database path required")
	}
	args := []string{"eval", "--db=" + c.DB}
	if c.Trajectory != "" {
		args = append(args, "--trajectory="+c.Trajectory)
	}
	if c.Assertions != "" {
		args = append(args, "--assertions="+c.Assertions)
	}
	if c.JudgesRun != "" {
		args = append(args, "--judges-run="+c.JudgesRun)
	}
	if c.JudgeEndpoint != "" {
		args = append(args, "--judge-endpoint="+c.JudgeEndpoint)
	}
	if c.JudgeAPIKey != "" {
		args = append(args, "--judge-api-key="+c.JudgeAPIKey)
	}
	return args, nil
}

// ServeConfig drives `proofspan serve`. The SCIM token goes to the
// environment — it must never appear in argv (visible in ps output).
type ServeConfig struct {
	DB        string
	Addr      string
	SCIM      bool
	SCIMToken string
}

func ServeArgs(c ServeConfig) ([]string, map[string]string, error) {
	if c.DB == "" {
		return nil, nil, fmt.Errorf("database path required")
	}
	addr := c.Addr
	if addr == "" {
		addr = "127.0.0.1:7400"
	}
	args := []string{"serve", "--db=" + c.DB, "--addr=" + addr}
	env := map[string]string{}
	if c.SCIM {
		args = append(args, "--scim")
		if c.SCIMToken != "" {
			env["PROOFSPAN_SCIM_TOKEN"] = c.SCIMToken
		}
	}
	return args, env, nil
}

// Runner executes the binary with args; each stdout/stderr line is streamed
// to onLine. Returns the exit code. The caller owns UI updates.
type Runner struct {
	Binary string
	// Dir is the working directory for the command. Empty = inherit.
	// The CLI's relative defaults (judges/manifest.json, registry/bin)
	// resolve from here, so the GUI sets it to the DB's directory.
	Dir string
}

// Run executes and streams. Nonzero exit is reported as (code, nil) — an
// eval gate failing IS the product working; errors are exec failures.
func (r *Runner) Run(args []string, extraEnv map[string]string, onLine func(line string)) (int, error) {
	if r.Binary == "" {
		r.Binary = ResolveBinary()
	}
	cmd := exec.Command(r.Binary, args...)
	cmd.Env = append(environ(), envSlice(extraEnv)...)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	cmd.Stderr = cmd.Stdout // merge into one stream
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %s: %w (is the binary on PATH? set PROOFSPAN_BIN)", r.Binary, err)
	}
	sc := NewLineScanner(stdout)
	for sc.Scan() {
		if onLine != nil {
			onLine(sc.Text())
		}
	}
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func environ() []string { return os.Environ() }

// NewLineScanner wraps a reader for line streaming (test seam).
func NewLineScanner(r io.Reader) *bufio.Scanner {
	return bufio.NewScanner(r)
}
