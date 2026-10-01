// Package wasm runs WASM assertion modules from the registry against
// trajectories. wazero: pure-Go runtime, no CGO, single binary stays intact.
//
// Module contract (span-correlation@1.0.0 pins it):
//
//	assert(payloadPtr, payloadLen) int32   // 0 = pass, 1 = fail, ≥2 = error
//
// payload is a JSON array of ATF spans.
package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

// Result is one assertion outcome over one trajectory.
type Result struct {
	AssertionID      string `json:"assertion_id"`
	AssertionVersion string `json:"assertion_version"`
	Status           string `json:"status"` // pass|fail|error
	Detail           string `json:"detail,omitempty"`
}

// Registry maps assertion id → version → compiled module.
type Registry struct {
	dir  string
	mu   sync.Mutex
	rt   wazero.Runtime
	mods map[string]map[string]api.Module // id → version → module
}

// NewRegistry loads modules from dir (layout: <id>@<version>.wasm files
// or a subdir per assertion). Modules are instantiated once and reused.
func NewRegistry(dir string) *Registry {
	return &Registry{
		dir:  dir,
		rt:   wazero.NewRuntime(context.Background()),
		mods: map[string]map[string]api.Module{},
	}
}

// Register compiles and instantiates an assertion module under (id, version).
func (r *Registry) Register(id, version, wasmPath string) error {
	bin, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", wasmPath, err)
	}
	ctx := context.Background()
	// Go wasip1 modules import wasi_snapshot_preview1; instantiate it first
	wasi_snapshot_preview1.MustInstantiate(ctx, r.rt)
	compiled, err := r.rt.CompileModule(ctx, bin)
	if err != nil {
		return fmt.Errorf("compile %s: %w", wasmPath, err)
	}
	// reactor modules (_initialize, not _start) must run their initializer
	cfg := wazero.NewModuleConfig().WithStartFunctions("_initialize")
	mod, err := r.rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		return fmt.Errorf("instantiate %s: %w", wasmPath, err)
	}
	r.mu.Lock()
	if r.mods[id] == nil {
		r.mods[id] = map[string]api.Module{}
	}
	r.mods[id][version] = mod
	r.mu.Unlock()
	return nil
}

// LoadDir registers every *.wasm in dir as "<name>@<version>" from the
// file name pattern <id>-<version>.wasm or <id>@<version>.wasm.
func (r *Registry) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".wasm" {
			continue
		}
		id, version, err := parseName(name[:len(name)-5])
		if err != nil {
			return err
		}
		if err := r.Register(id, version, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

func parseName(stem string) (string, string, error) {
	for _, sep := range []string{"@", "-"} {
		if i := lastIndex(stem, sep); i > 0 {
			return stem[:i], stem[i+len(sep):], nil
		}
	}
	return "", "", fmt.Errorf("wasm file %q: need <id>@<version>.wasm or <id>-<version>.wasm", stem)
}

func lastIndex(s, sep string) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// Run executes an assertion over a trajectory. Unknown id or wrong
// version pin → status "error", never a silent pass.
func (r *Registry) Run(ctx context.Context, id, version string, spans []schema.Span) Result {
	res := Result{AssertionID: id, AssertionVersion: version}
	r.mu.Lock()
	vers, ok := r.mods[id]
	r.mu.Unlock()
	if !ok {
		res.Status = "error"
		res.Detail = fmt.Sprintf("unknown assertion %q", id)
		return res
	}
	mod, ok := vers[version]
	if !ok {
		res.Status = "error"
		available := "none"
		if len(vers) > 0 {
			keys := make([]string, 0, len(vers))
			for v := range vers {
				keys = append(keys, v)
			}
			available = joinQuoted(keys)
		}
		res.Detail = fmt.Sprintf("assertion %q pinned at %s, not %s", id, available, version)
		return res
	}
	payload, err := json.Marshal(spans)
	if err != nil {
		res.Status = "error"
		res.Detail = err.Error()
		return res
	}
	fn := mod.ExportedFunction("assert")
	if fn == nil {
		res.Status = "error"
		res.Detail = "module exports no assert() function"
		return res
	}
	alloc := mod.ExportedFunction("alloc")
	if alloc == nil {
		res.Status = "error"
		res.Detail = "module exports no alloc() function"
		return res
	}
	out, err := alloc.Call(ctx, uint64(len(payload)))
	if err != nil {
		res.Status = "error"
		res.Detail = fmt.Sprintf("alloc() trap: %v", err)
		return res
	}
	ptr := uint32(out[0])
	if !mod.Memory().Write(ptr, payload) {
		res.Status = "error"
		res.Detail = "payload write outside module memory"
		return res
	}
	out, err = fn.Call(ctx, uint64(ptr), uint64(len(payload)))
	if err != nil {
		res.Status = "error"
		res.Detail = fmt.Sprintf("assert() trap: %v", err)
		return res
	}
	code := int32(out[0])
	switch code {
	case 0:
		res.Status = "pass"
	case 1:
		res.Status = "fail"
		res.Detail = "assertion returned fail"
	default:
		res.Status = "error"
		res.Detail = fmt.Sprintf("assertion returned code %d", code)
	}
	return res
}

func joinQuoted(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += "\"" + x + "\""
	}
	return out
}

// Close tears down the runtime.
func (r *Registry) Close(ctx context.Context) error {
	return r.rt.Close(ctx)
}
