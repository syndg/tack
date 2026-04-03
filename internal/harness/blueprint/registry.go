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

// Registry holds loaded blueprints and provides lookup by blueprint ID.
type Registry struct {
	blueprints map[string]*Blueprint
	mu         sync.RWMutex
}

// NewRegistry creates an empty blueprint registry.
func NewRegistry() *Registry {
	return &Registry{blueprints: make(map[string]*Blueprint)}
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
		r.blueprints[bp.ID] = &bp
		if err := r.validateDefaultSelectionLocked(); err != nil {
			r.mu.Unlock()
			errs = append(errs, fmt.Sprintf("registering %s: %v", entry.Name(), err))
			continue
		}
		r.mu.Unlock()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors loading defaults: %s", strings.Join(errs, "; "))
	}
	return nil
}

// LoadFromDir loads blueprints from a directory (e.g., .tack/blueprints/ or ~/.config/tack/blueprints/).
// Blueprints loaded later override earlier ones with the same blueprint ID.
func (r *Registry) LoadFromDir(dir string) error {
	entries, err := LoadDir(dir)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for id, bp := range entries {
		r.blueprints[id] = bp
	}
	if err := r.validateDefaultSelectionLocked(); err != nil {
		return err
	}
	return nil
}

// Get returns a blueprint by ID.
func (r *Registry) Get(id string) (*Blueprint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.blueprints[id]
	return bp, ok
}

// ResolveDefault returns the configured default blueprint, or the only loaded blueprint.
func (r *Registry) ResolveDefault() (*Blueprint, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	switch len(r.blueprints) {
	case 0:
		return nil, fmt.Errorf("no blueprints loaded")
	case 1:
		for _, bp := range r.blueprints {
			return bp, nil
		}
	}

	var defaults []*Blueprint
	for _, bp := range r.blueprints {
		if bp.Default {
			defaults = append(defaults, bp)
		}
	}
	switch len(defaults) {
	case 1:
		return defaults[0], nil
	case 0:
		return nil, fmt.Errorf("multiple blueprints are loaded but none is marked default; select one with --blueprint or set default: true")
	default:
		return nil, fmt.Errorf("multiple blueprints are marked default")
	}
}

// List returns all registered blueprint IDs, sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.blueprints))
	for id := range r.blueprints {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (r *Registry) validateDefaultSelectionLocked() error {
	defaultCount := 0
	for _, bp := range r.blueprints {
		if bp.Default {
			defaultCount++
		}
	}
	if defaultCount > 1 {
		return fmt.Errorf("multiple blueprints are marked default")
	}
	return nil
}
