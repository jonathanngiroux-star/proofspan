package app

import "strings"

// DBDir extracts the directory part of a db path ("." for bare names).
// The GUI/TUI run commands with this as working directory so the CLI's
// relative defaults (judges/manifest.json, registry/bin) resolve.
func DBDir(dbPath string) string {
	if i := strings.LastIndexByte(dbPath, '/'); i > 0 {
		return dbPath[:i]
	}
	return "."
}
