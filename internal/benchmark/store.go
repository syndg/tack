package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func runsPath(dataDir string) string {
	return filepath.Join(dataDir, "benchmarks", "runs.json")
}

func SaveRun(dataDir string, run Run) error {
	runs, err := LoadRuns(dataDir)
	if err != nil {
		return err
	}
	runs = append(runs, run)
	path := runsPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir benchmark data dir: %w", err)
	}
	raw, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal benchmark runs: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write benchmark runs: %w", err)
	}
	return nil
}

func LoadRuns(dataDir string) ([]Run, error) {
	path := runsPath(dataDir)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read benchmark runs: %w", err)
	}
	var runs []Run
	if err := json.Unmarshal(raw, &runs); err != nil {
		return nil, fmt.Errorf("decode benchmark runs: %w", err)
	}
	return runs, nil
}

func FindRun(dataDir, runID string) (Run, bool, error) {
	runs, err := LoadRuns(dataDir)
	if err != nil {
		return Run{}, false, err
	}
	for _, run := range runs {
		if run.ID == runID {
			return run, true, nil
		}
	}
	return Run{}, false, nil
}

func UpdateRun(dataDir string, updated Run) error {
	runs, err := LoadRuns(dataDir)
	if err != nil {
		return err
	}
	for i, run := range runs {
		if run.ID == updated.ID {
			runs[i] = updated
			path := runsPath(dataDir)
			raw, err := json.MarshalIndent(runs, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal benchmark runs: %w", err)
			}
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				return fmt.Errorf("write benchmark runs: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("benchmark run %q not found", updated.ID)
}
