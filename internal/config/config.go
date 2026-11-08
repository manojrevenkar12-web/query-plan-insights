// Package config loads config/qpi.json. Every threshold the detector and the diagnosis use lives there.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Collector struct {
	IntervalMillis             int `json:"intervalMillis"`
	ActivitySamplesPerInterval int `json:"activitySamplesPerInterval"`
	ExplainEverySeconds        int `json:"explainEverySeconds"`
	ExplainTopN                int `json:"explainTopN"`
	ExplainTimeoutMillis       int `json:"explainTimeoutMillis"`
	RetentionSeconds           int `json:"retentionSeconds"`
	MinCallsToTrack            int `json:"minCallsToTrack"`
}

type Detector struct {
	BaselineSeconds           int     `json:"baselineSeconds"`
	RecentSeconds             int     `json:"recentSeconds"`
	MinCalls                  int64   `json:"minCalls"`
	LatencyRatio              float64 `json:"latencyRatio"`
	MinDeltaMillis            float64 `json:"minDeltaMillis"`
	PersistTicks              int     `json:"persistTicks"`
	ResolveTolerance          float64 `json:"resolveTolerance"`
	WarmupSeconds             int     `json:"warmupSeconds"`
	NewQueryShareOfCalls      float64 `json:"newQueryShareOfCalls"`
	NewQueryMinCallsPerSecond float64 `json:"newQueryMinCallsPerSecond"`
	SpillTempBlocksPerCall    float64 `json:"spillTempBlocksPerCall"`
	SpillLatencyRatio         float64 `json:"spillLatencyRatio"`
}

type Diagnosis struct {
	EventLookbackSeconds    int     `json:"eventLookbackSeconds"`
	LockShareMin            float64 `json:"lockShareMin"`
	TempBlocksPerCallMin    float64 `json:"tempBlocksPerCallMin"`
	ReadRatioMin            float64 `json:"readRatioMin"`
	RowsRatioMin            float64 `json:"rowsRatioMin"`
	ModSinceAnalyzeShareMin float64 `json:"modSinceAnalyzeShareMin"`
	EstimateErrorMin        float64 `json:"estimateErrorMin"`
	ContentionRatioMin      float64 `json:"contentionRatioMin"`
}

type API struct {
	Listen        string `json:"listen"`
	OverviewRows  int    `json:"overviewRows"`
	WindowSeconds int    `json:"windowSeconds"`
}

// Eval is how the fault-injection evaluation runs each scenario.
type Eval struct {
	BaselineSeconds       int               `json:"baselineSeconds"`
	FaultSeconds          int               `json:"faultSeconds"`
	FixSeconds            int               `json:"fixSeconds"`
	ConnMaxLifetimeMillis int               `json:"connMaxLifetimeMillis"`
	ServerSettings        map[string]string `json:"serverSettings"`
	OverheadSeconds       int               `json:"overheadSeconds"`
	OverheadRounds        int               `json:"overheadRounds"`
	OverheadWorkers       int               `json:"overheadWorkers"`
	Seed                  uint64            `json:"seed"`
}

type Config struct {
	Collector Collector `json:"collector"`
	Detector  Detector  `json:"detector"`
	Diagnosis Diagnosis `json:"diagnosis"`
	API       API       `json:"api"`
	Eval      Eval      `json:"eval"`
}

func (c Collector) Interval() time.Duration {
	return time.Duration(c.IntervalMillis) * time.Millisecond
}

// Load reads a config file strictly: unknown fields are errors.
func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Root finds the repository root (the directory with config/qpi.json) from the working directory upwards.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "config", "qpi.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("config/qpi.json not found")
		}
		dir = parent
	}
}

// LoadDefault loads config/qpi.json from the repository root.
func LoadDefault() (Config, error) {
	root, err := Root()
	if err != nil {
		return Config{}, err
	}
	return Load(filepath.Join(root, "config", "qpi.json"))
}
