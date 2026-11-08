// Package eval injects real faults into a real PostgreSQL under a running workload, and scores what the collector
// found: whether it noticed, how fast, whether it named the cause and the fix, what it flagged that was fine, and
// whether it saw the fix work.
package eval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Holder is a session that takes locks and keeps its transaction open.
type Holder struct {
	SQL         string `json:"sql"`
	HoldMillis  int    `json:"holdMillis"`
	PauseMillis int    `json:"pauseMillis"`
}

// Action is a fault or its fix.
type Action struct {
	SQL            []string `json:"sql,omitempty"`
	LockHolder     *Holder  `json:"lockHolder,omitempty"`
	StopLockHolder bool     `json:"stopLockHolder,omitempty"`
	NPlusOne       *bool    `json:"nPlusOne,omitempty"`
}

// Scenario is one fault, what it should be found as, and how it is fixed. A scenario without a target is a control.
type Scenario struct {
	Name         string   `json:"name"`
	About        string   `json:"about"`
	App          string   `json:"app"`
	Target       string   `json:"target,omitempty"`
	AlsoAffected []string `json:"alsoAffected,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Cause        string   `json:"cause,omitempty"`
	Suggestion   string   `json:"suggestion,omitempty"`
	Fault        *Action  `json:"fault,omitempty"`
	Fix          *Action  `json:"fix,omitempty"`
}

// Control reports whether the scenario injects nothing.
func (s Scenario) Control() bool { return s.Target == "" }

// Spec is the development set, tuned on, and the held-out set, run once.
type Spec struct {
	Dev     []Scenario `json:"dev"`
	Heldout []Scenario `json:"heldout"`
}

// LoadSpec reads config/scenarios.json.
func LoadSpec(root string) (Spec, error) {
	var s Spec
	b, err := os.ReadFile(filepath.Join(root, "config", "scenarios.json"))
	if err != nil {
		return s, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("config/scenarios.json: %w", err)
	}
	for _, set := range [][]Scenario{s.Dev, s.Heldout} {
		for _, sc := range set {
			if err := sc.validate(); err != nil {
				return s, fmt.Errorf("scenario %s: %w", sc.Name, err)
			}
		}
	}
	return s, nil
}

func (s Scenario) validate() error {
	if s.App != "shop" && s.App != "billing" {
		return fmt.Errorf("unknown app %q", s.App)
	}
	if s.Control() {
		if s.Fault != nil || s.Fix != nil {
			return fmt.Errorf("a control has no fault and no fix")
		}
		return nil
	}
	if s.Fault == nil || s.Fix == nil || s.Kind == "" || s.Cause == "" || s.Suggestion == "" {
		return fmt.Errorf("a fault scenario needs kind, cause, suggestion, fault and fix")
	}
	return nil
}

// Pin is the hash of the held-out scenarios. It is written once, before any tuning, and checked on every held-out
// run, so the held-out set cannot drift towards what the detector already does.
func (s Spec) Pin() string {
	b, _ := json.Marshal(s.Heldout)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// CheckPin compares the held-out scenarios with config/heldout.sha256.
func CheckPin(root string, s Spec) error {
	b, err := os.ReadFile(filepath.Join(root, "config", "heldout.sha256"))
	if err != nil {
		return fmt.Errorf("held-out pin missing: %w", err)
	}
	if want := strings.TrimSpace(string(b)); want != s.Pin() {
		return fmt.Errorf("held-out scenarios changed since they were pinned (pin %s, now %s)", want[:12], s.Pin()[:12])
	}
	return nil
}
