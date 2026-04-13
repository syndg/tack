package discovery

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
)

func TestGenerateBuildsPersistedDossier(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, "pkg", "gui", "controllers"), 0o755); err != nil {
		t.Fatalf("MkdirAll controllers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "pkg", "gui", "controllers", "command_log_controller.go"), []byte("package controllers\n\nfunc commandLogKeybindings() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile controller: %v", err)
	}
	rulesDir := filepath.Join(projectRoot, ".tack", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll rules: %v", err)
	}
	ruleBody := "---\nscope: pkg/gui/**\npriority: high\n---\nKeep command log interactions inside the GUI controller layer.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "gui.md"), []byte(ruleBody), 0o644); err != nil {
		t.Fatalf("WriteFile rule: %v", err)
	}

	database, err := db.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx := context.Background()
	projectStore := db.NewProjectStore(database.Conn())
	project := &domain.Project{Name: "demo", RootPath: projectRoot, ConfigPath: filepath.Join(projectRoot, ".tack", "config.yaml")}
	if err := projectStore.Upsert(ctx, project); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	objectiveStore := db.NewObjectiveStore(database.Conn())
	objective := &domain.Objective{ProjectID: project.ID, Description: "Add command log keybindings", Blueprint: "build-review"}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	dossierStore := db.NewDossierStore(database.Conn())
	rulesEngine := rules.NewEngine(slog.Default())
	if err := rulesEngine.LoadDir(rulesDir); err != nil {
		t.Fatalf("LoadDir rules: %v", err)
	}
	registry := blueprint.NewRegistry()
	if err := registry.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	svc := New(projectRoot, objectiveStore, dossierStore, rulesEngine, registry, slog.Default())
	dossier, err := svc.Generate(ctx, objective.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if dossier.BlueprintID != "build-review" {
		t.Fatalf("BlueprintID = %q, want build-review", dossier.BlueprintID)
	}
	if strings.TrimSpace(dossier.Summary) == "" {
		t.Fatal("expected non-empty dossier summary")
	}
	if len(dossier.RepoPriors) == 0 {
		t.Fatal("expected repo priors in dossier")
	}
	if len(dossier.RelevantFiles) == 0 || dossier.RelevantFiles[0].Path != "pkg/gui/controllers/command_log_controller.go" {
		t.Fatalf("relevant files = %#v, want command_log_controller.go first", dossier.RelevantFiles)
	}
	persisted, err := dossierStore.GetByObjective(ctx, objective.ID)
	if err != nil {
		t.Fatalf("GetByObjective: %v", err)
	}
	if persisted.Summary != dossier.Summary {
		t.Fatalf("persisted summary = %q, want %q", persisted.Summary, dossier.Summary)
	}
}
