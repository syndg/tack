package runtimecatalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type fakeRunner struct {
	paths map[string]string
	runs  []string
	out   map[string][]byte
	err   map[string]error
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if path := f.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("not found")
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name
	for _, arg := range args {
		key += " " + arg
	}
	f.runs = append(f.runs, key)
	if err := f.err[key]; err != nil {
		return nil, err
	}
	return f.out[key], nil
}

func TestProbePiReportsMissingAndPackageManagers(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"bun": "/opt/bin/bun"}}
	probe := ProbePi(context.Background(), runner)
	if probe.Installed {
		t.Fatal("expected Pi to be missing")
	}
	if !reflect.DeepEqual(probe.PackageManagers, []string{"bun"}) {
		t.Fatalf("PackageManagers = %v, want [bun]", probe.PackageManagers)
	}
}

func TestPlanPiInstallRequiresPackageManager(t *testing.T) {
	_, err := PlanPiInstall(context.Background(), &fakeRunner{paths: map[string]string{}}, true)
	if err == nil || err.Error() != "Pi installation requires Node.js/npm or Bun; neither npm nor bun was found" {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanPiInstallRequiresConsentBeforeRunning(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"npm": "/usr/bin/npm"}}
	plan, err := PlanPiInstall(context.Background(), runner, false)
	if err != nil {
		t.Fatalf("PlanPiInstall: %v", err)
	}
	if !plan.Needed || !plan.RequiresConsent || plan.PackageManager != "npm" {
		t.Fatalf("plan = %#v", plan)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %v, want no install before consent", runner.runs)
	}
}

func TestPlanPiInstallRunsAfterConsent(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"bun": "/opt/bin/bun"}, out: map[string][]byte{"bun add -g @mariozechner/pi-coding-agent": []byte("ok")}}
	plan, err := PlanPiInstall(context.Background(), runner, true)
	if err != nil {
		t.Fatalf("PlanPiInstall: %v", err)
	}
	if plan.RequiresConsent || plan.PackageManager != "bun" {
		t.Fatalf("plan = %#v", plan)
	}
	if !reflect.DeepEqual(runner.runs, []string{"bun add -g @mariozechner/pi-coding-agent"}) {
		t.Fatalf("runs = %v", runner.runs)
	}
}

func TestProbePiParsesLiveCatalog(t *testing.T) {
	runner := &fakeRunner{
		paths: map[string]string{"pi": "/usr/local/bin/pi"},
		out:   map[string][]byte{"pi --list-models": []byte("provider model context max-out thinking images\nanthropic claude-opus-4-1 200K 32K yes yes\nopenai gpt-5 272K 128K yes no\n")},
	}
	probe := ProbePi(context.Background(), runner)
	if !probe.Installed || probe.Path != "/usr/local/bin/pi" {
		t.Fatalf("probe = %#v", probe)
	}
	if len(probe.Catalog) != 2 || probe.Catalog[0].Provider != "anthropic" || probe.Catalog[0].Models[0].ID != "claude-opus-4-1" {
		t.Fatalf("Catalog = %#v", probe.Catalog)
	}
	if probe.Catalog[0].Models[0].Label == probe.Catalog[0].Models[0].ID {
		t.Fatalf("expected rich label, got %#v", probe.Catalog[0].Models[0])
	}
}

func TestProbePiReportsCatalogFailure(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"pi": "/usr/local/bin/pi"}, err: map[string]error{"pi --list-models": errors.New("boom")}}
	probe := ProbePi(context.Background(), runner)
	if probe.CatalogError == "" {
		t.Fatalf("probe = %#v, want catalog error", probe)
	}
}

func TestCatalogLookupHelpers(t *testing.T) {
	catalog := []ProviderCatalog{
		{Provider: "openai", Models: []Model{{ID: "gpt-5"}}},
		{Provider: "anthropic", Models: []Model{{ID: "claude-opus-4-1"}, {ID: "claude-haiku"}}},
	}
	if !CatalogHasProvider(catalog, "anthropic") || CatalogHasProvider(catalog, "missing") {
		t.Fatalf("provider lookup failed")
	}
	if !CatalogHasModel(catalog, "anthropic", "claude-haiku") || CatalogHasModel(catalog, "anthropic", "gpt-5") {
		t.Fatalf("model lookup failed")
	}
	if got := CatalogProviderNames(catalog); !reflect.DeepEqual(got, []string{"anthropic", "openai"}) {
		t.Fatalf("CatalogProviderNames = %v", got)
	}
	if got := CatalogModelIDs(catalog, "anthropic"); !reflect.DeepEqual(got, []string{"claude-haiku", "claude-opus-4-1"}) {
		t.Fatalf("CatalogModelIDs = %v", got)
	}
}

func TestDaemonCanSeePiReportsHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	ok, evidence := DaemonCanSeePi(context.Background(), server.URL)
	if !ok || evidence == "" {
		t.Fatalf("ok=%t evidence=%q", ok, evidence)
	}
}

func TestDaemonCanSeePiReportsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	ok, evidence := DaemonCanSeePi(context.Background(), server.URL)
	if ok || evidence != "daemon health returned HTTP 503" {
		t.Fatalf("ok=%t evidence=%q", ok, evidence)
	}
}
