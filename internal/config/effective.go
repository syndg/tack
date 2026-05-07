package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type ValueSource string

const (
	ValueSourceMissing ValueSource = "missing"
	ValueSourceGlobal  ValueSource = "global"
	ValueSourceProject ValueSource = "project_override"
)

type EffectiveString struct {
	Value  string
	Source ValueSource
	Set    bool
}

type EffectiveStringSlice struct {
	Value  []string
	Source ValueSource
	Set    bool
}

type EffectiveConfig struct {
	Runtime         EffectiveString
	Provider        EffectiveString
	AuthMode        EffectiveString
	AuthMethod      EffectiveString
	CredentialRef   EffectiveString
	SandboxProvider EffectiveString
	Blueprint       EffectiveString
	AgentModel      EffectiveString
	PlannerModel    EffectiveString
	SmallTaskModel  EffectiveString
	QualityGates    EffectiveStringSlice
	ProjectRoot     string
}

// ResolveEffective computes explicit project-over-global config values with
// field-level provenance. It does not apply built-in defaults or write files.
func ResolveEffective(projectPath, userPath string) (*EffectiveConfig, error) {
	if err := ValidateProjectConfigPath(projectPath); err != nil {
		return nil, err
	}

	global, err := loadExplicitConfig(userPath)
	if err != nil {
		return nil, fmt.Errorf("loading user config: %w", err)
	}
	project, err := loadExplicitConfig(projectPath)
	if err != nil {
		return nil, fmt.Errorf("loading project config: %w", err)
	}

	effective := &EffectiveConfig{
		Runtime:         pickString(project.Agents.Runtime, global.Agents.Runtime),
		Provider:        pickString(project.RuntimeAuth.Provider, global.RuntimeAuth.Provider),
		AuthMode:        pickString(project.RuntimeAuth.Mode, global.RuntimeAuth.Mode),
		AuthMethod:      pickString(project.RuntimeAuth.Method, global.RuntimeAuth.Method),
		CredentialRef:   pickString(project.RuntimeAuth.CredentialRef, global.RuntimeAuth.CredentialRef),
		SandboxProvider: pickString(project.Sandbox.Provider, global.Sandbox.Provider),
		Blueprint:       pickString(project.Blueprint, global.Blueprint),
		AgentModel:      pickString(project.Models.Agent, global.Models.Agent),
		PlannerModel:    pickString(project.Models.Planner, global.Models.Planner),
		SmallTaskModel:  pickString(project.Models.SmallTasks, global.Models.SmallTasks),
		QualityGates:    pickStringSlice(project.QualityGates, global.QualityGates),
	}
	if projectPath != "" {
		effective.ProjectRoot = effectiveProjectRoot(projectPath)
	}
	return effective, nil
}

func effectiveProjectRoot(projectPath string) string {
	expanded := expandTilde(projectPath)
	dir := filepath.Dir(expanded)
	if filepath.Base(expanded) == ProjectConfigFileName && filepath.Base(dir) == ProjectConfigDir {
		return filepath.Dir(dir)
	}
	return dir
}

func loadExplicitConfig(path string) (*Config, error) {
	cfg := &Config{}
	if path == "" {
		return cfg, nil
	}
	path = expandTilde(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

func pickString(projectValue, globalValue string) EffectiveString {
	if projectValue != "" {
		return EffectiveString{Value: projectValue, Source: ValueSourceProject, Set: true}
	}
	if globalValue != "" {
		return EffectiveString{Value: globalValue, Source: ValueSourceGlobal, Set: true}
	}
	return EffectiveString{Source: ValueSourceMissing}
}

func pickStringSlice(projectValue, globalValue []string) EffectiveStringSlice {
	if projectValue != nil {
		return EffectiveStringSlice{Value: append([]string(nil), projectValue...), Source: ValueSourceProject, Set: true}
	}
	if globalValue != nil {
		return EffectiveStringSlice{Value: append([]string(nil), globalValue...), Source: ValueSourceGlobal, Set: true}
	}
	return EffectiveStringSlice{Source: ValueSourceMissing}
}
