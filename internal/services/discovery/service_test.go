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

func TestExpandDossierAddsRequestedContext(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, "pkg", "gui", "controllers"), 0o755); err != nil {
		t.Fatalf("MkdirAll controllers: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectRoot, "pkg", "gui", "keys"), 0o755); err != nil {
		t.Fatalf("MkdirAll keys: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "pkg", "gui", "controllers", "command_log_controller.go"), []byte("package controllers\n\nfunc commandLogKeybindings() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile controller: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "pkg", "gui", "keys", "command_log_keybindings.go"), []byte("package keys\n\nfunc commandLogKeys() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile keybindings: %v", err)
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
	objective := &domain.Objective{ProjectID: project.ID, Description: "Improve controller behavior", Blueprint: "build-review"}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	dossierStore := db.NewDossierStore(database.Conn())
	registry := blueprint.NewRegistry()
	if err := registry.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	svc := New(projectRoot, objectiveStore, dossierStore, rules.NewEngine(slog.Default()), registry, slog.Default())
	initial, err := svc.Generate(ctx, objective.ID)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	expanded, err := svc.ExpandDossier(ctx, objective.ID, domain.DossierExpansionRequest{
		Reason:     "Need concrete command log keybinding evidence",
		FocusAreas: []string{"command log keybindings"},
		Questions:  []string{"Which files define command log navigation keys?"},
	})
	if err != nil {
		t.Fatalf("ExpandDossier: %v", err)
	}
	if len(expanded.RelevantFiles) <= len(initial.RelevantFiles) {
		t.Fatalf("expanded relevant files = %d, want > %d", len(expanded.RelevantFiles), len(initial.RelevantFiles))
	}
	paths := make([]string, 0, len(expanded.RelevantFiles))
	for _, ref := range expanded.RelevantFiles {
		paths = append(paths, ref.Path)
	}
	if !strings.Contains(strings.Join(paths, "\n"), "command_log") {
		t.Fatalf("expanded relevant files = %#v, want command log matches", expanded.RelevantFiles)
	}
	if !strings.Contains(strings.Join(expanded.Unknowns, "\n"), "Which files define command log navigation keys?") {
		t.Fatalf("expanded unknowns = %#v, want planner question", expanded.Unknowns)
	}
	if len(expanded.Citations) <= len(initial.Citations) {
		t.Fatalf("expanded citations = %d, want > %d", len(expanded.Citations), len(initial.Citations))
	}
	if expanded.UpdatedAt.Before(initial.UpdatedAt) {
		t.Fatalf("expanded updated_at = %v, initial = %v", expanded.UpdatedAt, initial.UpdatedAt)
	}
}

func TestUpdateDossierRecordsInsight(t *testing.T) {
	projectRoot := t.TempDir()
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
	objective := &domain.Objective{ProjectID: project.ID, Description: "Improve dossier edits", Blueprint: "build-review"}
	if err := objectiveStore.Create(ctx, objective); err != nil {
		t.Fatalf("Create objective: %v", err)
	}
	dossierStore := db.NewDossierStore(database.Conn())
	insightStore := db.NewObjectiveInsightStore(database.Conn())
	registry := blueprint.NewRegistry()
	if err := registry.LoadDefaults(); err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	svc := New(projectRoot, objectiveStore, dossierStore, rules.NewEngine(slog.Default()), registry, slog.Default())
	svc.BindInsightStore(insightStore)
	if _, err := svc.Generate(ctx, objective.ID); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	updated, err := svc.UpdateDossier(ctx, objective.ID, domain.Dossier{Summary: "Edited dossier summary", Risks: []string{"manual edit"}})
	if err != nil {
		t.Fatalf("UpdateDossier: %v", err)
	}
	if updated.Summary != "Edited dossier summary" {
		t.Fatalf("summary = %q", updated.Summary)
	}
	insights, err := insightStore.ListByObjective(ctx, objective.ID, 10)
	if err != nil {
		t.Fatalf("ListByObjective: %v", err)
	}
	if len(insights) != 1 || insights[0].Kind != domain.InsightKindDossierEdit {
		t.Fatalf("insights = %#v", insights)
	}
	if !strings.Contains(insights[0].Summary, "summary") || !strings.Contains(insights[0].Summary, "risks") {
		t.Fatalf("summary = %q", insights[0].Summary)
	}
}
