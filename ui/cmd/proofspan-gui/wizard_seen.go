package main

import (
	"os"
	"path/filepath"
)

// wizardSeenPath is the marker file recording "the user finished or
// skipped the first-run wizard". We deliberately do NOT use webview
// localStorage: WebKitGTK can refuse storage writes in the Wails
// custom-scheme context (the exact failure that stranded the Skip button),
// which would both break dismissing AND re-open the modal every launch.
// A file next to the OS config dir is reliable everywhere Wails runs.
func wizardSeenPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		if home, herr := os.UserHomeDir(); herr == nil {
			dir = filepath.Join(home, ".config")
		} else {
			dir = "."
		}
	}
	return filepath.Join(dir, "proofspan", "wizard-seen")
}

// WizardMarkSeen records that the walkthrough is done (close or skip).
func (g *GUI) WizardMarkSeen() {
	_ = os.MkdirAll(filepath.Dir(wizardSeenPath()), 0o755)
	_ = os.WriteFile(wizardSeenPath(), []byte("seen\n"), 0o644)
}

// WizardHasSeen reports whether the walkthrough was completed or skipped.
func (g *GUI) WizardHasSeen() bool {
	_, err := os.Stat(wizardSeenPath())
	return err == nil
}
