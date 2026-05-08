package runtimecatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/syndg/tack/internal/daemonauth"
)

const PiPackage = "@mariozechner/pi-coding-agent"

type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type PiProbe struct {
	Installed       bool
	Path            string
	Version         string
	PackageManagers []string
	Catalog         []ProviderCatalog
	CatalogError    string
}

type ProviderCatalog struct {
	Provider string
	Models   []Model
}

type Model struct {
	ID       string
	Label    string
	Context  string
	MaxOut   string
	Thinking string
	Images   string
}

type InstallPlan struct {
	Needed          bool
	RequiresConsent bool
	PackageManager  string
	Command         []string
}

func ProbePi(ctx context.Context, runner Runner) PiProbe {
	if runner == nil {
		runner = ExecRunner{}
	}
	probe := PiProbe{PackageManagers: availablePackageManagers(runner)}
	if path, err := runner.LookPath("pi"); err == nil && path != "" {
		probe.Installed = true
		probe.Path = path
		if out, err := runner.Run(ctx, "pi", "--version"); err == nil {
			probe.Version = strings.TrimSpace(string(out))
		}
		catalog, err := QueryPiCatalog(ctx, runner)
		if err != nil {
			probe.CatalogError = err.Error()
		} else {
			probe.Catalog = catalog
		}
	}
	return probe
}

func PlanPiInstall(ctx context.Context, runner Runner, consent bool) (InstallPlan, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	if path, err := runner.LookPath("pi"); err == nil && path != "" {
		return InstallPlan{}, nil
	}
	managers := availablePackageManagers(runner)
	if len(managers) == 0 {
		return InstallPlan{}, fmt.Errorf("Pi installation requires Node.js/npm or Bun; neither npm nor bun was found")
	}
	manager := managers[0]
	plan := InstallPlan{Needed: true, RequiresConsent: !consent, PackageManager: manager, Command: piInstallCommand(manager)}
	if !consent {
		return plan, nil
	}
	if _, err := runner.Run(ctx, plan.Command[0], plan.Command[1:]...); err != nil {
		return plan, fmt.Errorf("installing Pi with %s: %w", manager, err)
	}
	return plan, nil
}

func QueryPiCatalog(ctx context.Context, runner Runner) ([]ProviderCatalog, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	stdout, err := runner.Run(ctx, "pi", "--list-models")
	if err != nil {
		return nil, fmt.Errorf("querying Pi model catalog: %w", err)
	}
	return ParsePiCatalog(string(stdout)), nil
}

func ParsePiCatalog(output string) []ProviderCatalog {
	byProvider := map[string][]Model{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "provider" {
			continue
		}
		model := Model{ID: fields[1], Label: fields[1]}
		if len(fields) >= 3 {
			model.Context = fields[2]
		}
		if len(fields) >= 4 {
			model.MaxOut = fields[3]
		}
		if len(fields) >= 5 {
			model.Thinking = fields[4]
		}
		if len(fields) >= 6 {
			model.Images = fields[5]
		}
		parts := []string{}
		if model.Context != "" {
			parts = append(parts, model.Context+" ctx")
		}
		if model.Thinking != "" {
			parts = append(parts, model.Thinking+" thinking")
		}
		if model.Images != "" {
			parts = append(parts, model.Images+" images")
		}
		if len(parts) > 0 {
			model.Label = fmt.Sprintf("%s (%s)", model.ID, strings.Join(parts, ", "))
		}
		byProvider[fields[0]] = append(byProvider[fields[0]], model)
	}
	providers := make([]ProviderCatalog, 0, len(byProvider))
	for provider, models := range byProvider {
		providers = append(providers, ProviderCatalog{Provider: provider, Models: models})
	}
	slices.SortFunc(providers, func(a, b ProviderCatalog) int {
		return strings.Compare(a.Provider, b.Provider)
	})
	return providers
}

func CatalogHasProvider(catalog []ProviderCatalog, provider string) bool {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return false
	}
	for _, entry := range catalog {
		if entry.Provider == provider {
			return true
		}
	}
	return false
}

func CatalogHasModel(catalog []ProviderCatalog, provider, model string) bool {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return false
	}
	for _, entry := range catalog {
		if entry.Provider != provider {
			continue
		}
		for _, candidate := range entry.Models {
			if candidate.ID == model {
				return true
			}
		}
		return false
	}
	return false
}

func CatalogProviderNames(catalog []ProviderCatalog) []string {
	names := make([]string, 0, len(catalog))
	for _, entry := range catalog {
		names = append(names, entry.Provider)
	}
	slices.Sort(names)
	return names
}

func CatalogModelIDs(catalog []ProviderCatalog, provider string) []string {
	for _, entry := range catalog {
		if entry.Provider != provider {
			continue
		}
		ids := make([]string, 0, len(entry.Models))
		for _, model := range entry.Models {
			ids = append(ids, model.ID)
		}
		slices.Sort(ids)
		return ids
	}
	return nil
}

func DaemonCanSeePi(ctx context.Context, listen string) (bool, string) {
	listen = normalizeListen(listen)
	if listen == "" {
		return false, "daemon.listen is empty"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listen+"/runtime/pi/visibility", nil)
	if err != nil {
		return false, err.Error()
	}
	if token, err := daemonauth.Load(); err == nil && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("daemon Pi visibility returned HTTP %d", resp.StatusCode)
	}
	var probe PiProbe
	if err := json.Unmarshal(body, &probe); err != nil {
		return false, fmt.Sprintf("decoding daemon Pi visibility: %v", err)
	}
	if !probe.Installed {
		return false, "pi executable was not found in daemon PATH"
	}
	if probe.Version == "" {
		return true, fmt.Sprintf("daemon can see Pi at %s", probe.Path)
	}
	return true, fmt.Sprintf("daemon can see Pi at %s (%s)", probe.Path, probe.Version)
}

func availablePackageManagers(runner Runner) []string {
	managers := []string{}
	for _, name := range []string{"bun", "npm"} {
		if path, err := runner.LookPath(name); err == nil && path != "" {
			managers = append(managers, name)
		}
	}
	return managers
}

func piInstallCommand(manager string) []string {
	if manager == "bun" {
		return []string{"bun", "add", "-g", PiPackage}
	}
	return []string{"npm", "install", "-g", PiPackage}
}

func normalizeListen(listen string) string {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return ""
	}
	if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
		return strings.TrimRight(listen, "/")
	}
	return "http://" + strings.TrimRight(listen, "/")
}
