package blueprint

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed defaults/*.yaml
var defaultBlueprints embed.FS

// Registry holds loaded blueprints and provides lookup.
type Registry struct {
	blueprints map[string]*Blueprint
	aliases    map[string]string
	mu         sync.RWMutex
}

// NewRegistry creates an empty blueprint registry.
func NewRegistry() *Registry {
	return &Registry{
		blueprints: make(map[string]*Blueprint),
		aliases:    make(map[string]string),
	}
}

// LoadDefaults loads the shipped default blueprints from the embedded defaults/ directory.
func (r *Registry) LoadDefaults() error {
	entries, err := defaultBlueprints.ReadDir("defaults")
	if err != nil {
		return fmt.Errorf("reading embedded defaults: %w", err)
	}

	var errs []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		data, err := defaultBlueprints.ReadFile("defaults/" + entry.Name())
		if err != nil {
			errs = append(errs, fmt.Sprintf("reading %s: %v", entry.Name(), err))
			continue
		}

		var bp Blueprint
		if err := yaml.Unmarshal(data, &bp); err != nil {
			errs = append(errs, fmt.Sprintf("parsing %s: %v", entry.Name(), err))
			continue
		}

		if err := Validate(&bp); err != nil {
			errs = append(errs, fmt.Sprintf("validating %s: %v", entry.Name(), err))
			continue
		}

		r.mu.Lock()
		r.registerLocked(&bp, []string{bp.Name, strings.TrimSuffix(entry.Name(), ext)})
		r.mu.Unlock()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors loading defaults: %s", strings.Join(errs, "; "))
	}

	return nil
}

// LoadFromDir loads blueprints from a directory (e.g., .tack/blueprints/ or ~/.config/tack/blueprints/).
// Blueprints loaded later override earlier ones with the same name.
func (r *Registry) LoadFromDir(dir string) error {
	entries, err := LoadDir(dir)
	if err != nil {
		return err
	}

	r.mu.Lock()
	for name, bp := range entries {
		r.registerLocked(bp, []string{name})
	}
	r.mu.Unlock()

	return nil
}

// Get returns a blueprint by name or normalized alias. Returns nil, false if not found.
func (r *Registry) Get(name string) (*Blueprint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if bp, ok := r.blueprints[name]; ok {
		return bp, true
	}

	if canonical, ok := r.aliases[normalizeBlueprintKey(name)]; ok {
		bp, ok := r.blueprints[canonical]
		return bp, ok
	}

	return nil, false
}

// GetDefault returns the blueprint with trigger "default". Returns nil, false if none.
func (r *Registry) GetDefault() (*Blueprint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, bp := range r.blueprints {
		if bp.Trigger == "default" {
			return bp, true
		}
	}
	return nil, false
}

// List returns all registered blueprint names, sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.blueprints))
	for name := range r.blueprints {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *Registry) registerLocked(bp *Blueprint, aliases []string) {
	r.blueprints[bp.Name] = bp

	registerAlias := func(alias string) {
		key := normalizeBlueprintKey(alias)
		if key != "" {
			r.aliases[key] = bp.Name
		}
	}

	registerAlias(bp.Name)
	for _, alias := range aliases {
		registerAlias(alias)
	}
}

func normalizeBlueprintKey(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(b.String(), "-")
}
