package runtimecatalog

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"
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

func DaemonCanSeePi(ctx context.Context, listen string) (bool, string) {
	listen = normalizeListen(listen)
	if listen == "" {
		return false, "daemon.listen is empty"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listen+"/health", nil)
	if err != nil {
		return false, err.Error()
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("daemon health returned HTTP %d", resp.StatusCode)
	}
	return true, "daemon is healthy; Pi visibility is validated by the daemon process environment at execution time"
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
