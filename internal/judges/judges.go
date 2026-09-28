// Package judges loads and validates the judge registry manifest.
// Every judge must pin a model_fingerprint — uncontrolled LLM-as-judge
// drift is a Series A killer, so validation is strict and errors name
// the offending field.
package judges

import (
	"encoding/json"
	"fmt"
	"os"
)

// Manifest is the judge registry file (judges/manifest.json).
type Manifest struct {
	Namespace string  `json:"namespace"`
	Version   string  `json:"version"`
	Judges    []Judge `json:"judges"`
}

// Judge is one pinned LLM judge.
type Judge struct {
	ID               string `json:"id"`
	ModelFingerprint string `json:"model_fingerprint"`
	Description      string `json:"description"`
}

// Load reads and validates a manifest. All rules are enforced here,
// before any assertion runs.
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if m.Namespace == "" {
		return nil, fmt.Errorf("manifest %s: namespace required", path)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("manifest %s: version required", path)
	}
	if len(m.Judges) == 0 {
		return nil, fmt.Errorf("manifest %s: at least one judge required", path)
	}
	seen := map[string]bool{}
	for i, j := range m.Judges {
		if j.ID == "" {
			return nil, fmt.Errorf("manifest %s: judges[%d].id required", path, i)
		}
		if seen[j.ID] {
			return nil, fmt.Errorf("manifest %s: duplicate judge id %q", path, j.ID)
		}
		seen[j.ID] = true
		if j.ModelFingerprint == "" {
			return nil, fmt.Errorf("manifest %s: judge %q: model_fingerprint required (pinned judges only)", path, j.ID)
		}
	}
	return &m, nil
}

// Fingerprint returns the pinned fingerprint for a judge ID ("" if unknown).
func (m *Manifest) Fingerprint(judgeID string) string {
	for _, j := range m.Judges {
		if j.ID == judgeID {
			return j.ModelFingerprint
		}
	}
	return ""
}
