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
	mu         sync.RWMutex
}

// NewRegistry creates an empty blueprint registry.
func NewRegistry() *Registry {
	return &Registry{
		blueprints: make(map[string]*Blueprint),
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
		r.blueprints[bp.Name] = &bp
		r.mu.Unlock()
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors loading defaults: %s", strings.Join(errs, "; "))
	}

	return nil
}

// LoadFromDir loads blueprints from a directory (e.g., .deck/blueprints/ or ~/.config/deck/blueprints/).
// Blueprints loaded later override earlier ones with the same name.
func (r *Registry) LoadFromDir(dir string) error {
	loaded, err := LoadDir(dir)
	if err != nil {
		return err
	}

	r.mu.Lock()
	for name, bp := range loaded {
		r.blueprints[name] = bp
	}
	r.mu.Unlock()

	return nil
}

// Get returns a blueprint by name. Returns nil, false if not found.
func (r *Registry) Get(name string) (*Blueprint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	bp, ok := r.blueprints[name]
	return bp, ok
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
