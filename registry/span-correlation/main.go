// span-correlation@1.0.0 — the first WASM assertion in the registry.
//
// Verifies replay integrity of a trajectory:
//   - every span's parent_span_id, when set, must exist in the trajectory
//   - a child span must not start before its parent ends (chain order)
//   - an empty trajectory is trivially correlated
//
// Contract: alloc(size) int32 returns a scratch pointer into the module's
// linear-memory arena; the host writes the payload there, then calls
// assert(ptr, len) int32; 0 = pass, 1 = fail, 2 = error.
// Payload: JSON array of ATF v0.1.1 spans (subset of fields read).
//
// Build: GOOS=wasip1 GOARCH=wasm go build -o span-correlation@1.0.0.wasm .
package main

import (
	"encoding/json"
	"unsafe"
)

const arenaSize = 1 << 20 // 1 MiB scratch

type span struct {
	SpanID          string `json:"span_id"`
	ParentSpanID    string `json:"parent_span_id"`
	StartedAtUnixMs int64  `json:"started_at_unix_ms"`
	EndedAtUnixMs   int64  `json:"ended_at_unix_ms"`
}

//go:wasmexport alloc
func alloc(size int32) int32 {
	if size < 0 || int(size) > len(arena) {
		return 0
	}
	// address of a package-level array: stable, in linear memory
	return int32(uintptr(unsafe.Pointer(&arena[0])))
}

var arena [arenaSize]byte

//go:wasmexport assert
func assert(ptr int32, size int32) int32 {
	if ptr == 0 || size < 0 {
		return 2
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(size))
	var spans []span
	if err := json.Unmarshal(buf, &spans); err != nil {
		return 2
	}
	byID := make(map[string]span, len(spans))
	for _, s := range spans {
		byID[s.SpanID] = s
	}
	for _, s := range spans {
		if s.ParentSpanID == "" {
			continue
		}
		p, ok := byID[s.ParentSpanID]
		if !ok {
			return 1 // orphan: parent missing from trajectory
		}
		if s.StartedAtUnixMs < p.EndedAtUnixMs {
			return 1 // chain order violated: child starts before parent ends
		}
	}
	return 0
}

func main() {} // required for package main; never executed (exports only)
