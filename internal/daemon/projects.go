package daemon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/syndg/tack/internal/config"
	"github.com/syndg/tack/internal/domain"
)

const projectHeader = "X-Tack-Project-ID"

func (d *Daemon) targetProjectID(r *http.Request) string {
	if projectID := strings.TrimSpace(r.Header.Get(projectHeader)); projectID != "" {
		return projectID
	}
	return strings.TrimSpace(r.URL.Query().Get("project_id"))
}

func wantsAllProjects(r *http.Request) bool {
	v := strings.TrimSpace(r.URL.Query().Get("all"))
	return v == "1" || strings.EqualFold(v, "true")
}

func (d *Daemon) requireProjectContext(w http.ResponseWriter, r *http.Request) (*ProjectContext, bool) {
	projectID := d.targetProjectID(r)
	if projectID == "" {
		projects, err := d.projectStore.List(r.Context())
		if err == nil && len(projects) == 1 {
			projectID = projects[0].ID
		} else {
			writeError(w, http.StatusBadRequest, "project target required; run from a registered repo or pass --project")
			return nil, false
		}
	}
	ctx, err := d.projectCtxs.Get(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not registered; run tack init or tack project add")
			return nil, false
		}
		d.logger.Error("loading project context", "project_id", projectID, "error", err)
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("project configuration invalid: %v", err))
		return nil, false
	}
	return ctx, true
}

func (d *Daemon) ensureProjectMatch(w http.ResponseWriter, r *http.Request, projectID string) bool {
	target := d.targetProjectID(r)
	if target == "" || target == projectID || wantsAllProjects(r) {
		return true
	}
	writeError(w, http.StatusNotFound, "resource not found in targeted project")
	return false
}

func (d *Daemon) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := d.projectStore.List(r.Context())
	if err != nil {
		d.logger.Error("listing projects", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}
	if projects == nil {
		projects = []domain.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (d *Daemon) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, err := d.projectStore.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		d.logger.Error("getting project", "id", r.PathValue("id"), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get project")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (d *Daemon) handleResolveProject(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	project, err := d.projectStore.ResolvePath(r.Context(), path)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not registered; run tack init or tack project add")
			return
		}
		d.logger.Error("resolving project", "path", path, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resolve project")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (d *Daemon) handleRegisterProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string `json:"project_id"`
		Name       string `json:"name"`
		RootPath   string `json:"root_path"`
		ConfigPath string `json:"config_path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := normalizeProjectRegistration(req.ID, req.Name, req.RootPath, req.ConfigPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateProjectRegistration(project); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := config.Load(project.ConfigPath, d.projectCtxs.userConfigPath); err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("invalid project config: %v", err))
		return
	}
	if err := d.projectStore.Upsert(r.Context(), project); err != nil {
		d.logger.Error("registering project", "root", project.RootPath, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to register project")
		return
	}
	d.projectCtxs.Invalidate(project.ID)
	if _, err := d.projectCtxs.Get(r.Context(), project.ID); err != nil {
		d.projectCtxs.Invalidate(project.ID)
		_ = d.projectStore.Remove(r.Context(), project.ID)
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("failed to activate project: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (d *Daemon) handleRelinkProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	var req struct {
		RootPath   string `json:"root_path"`
		ConfigPath string `json:"config_path"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project, err := normalizeProjectRegistration(projectID, "", req.RootPath, req.ConfigPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateProjectRegistration(project); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := d.projectStore.Relink(r.Context(), projectID, project.RootPath, project.ConfigPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		d.logger.Error("relinking project", "project_id", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to relink project")
		return
	}
	d.projectCtxs.Invalidate(projectID)
	ctx, err := d.projectCtxs.Get(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("failed to activate relinked project: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, ctx.Project)
}

func (d *Daemon) handleRemoveProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	d.projectCtxs.Invalidate(projectID)
	if err := d.projectStore.Remove(r.Context(), projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		d.logger.Error("removing project", "project_id", projectID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove project")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func normalizeProjectRegistration(id, name, rootPath, configPath string) (*domain.Project, error) {
	rootPath = strings.TrimSpace(rootPath)
	if rootPath == "" {
		return nil, fmt.Errorf("root_path is required")
	}
	rootPath = filepath.Clean(rootPath)
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolving root path: %w", err)
	}
	if configPath == "" {
		configPath = config.ProjectConfigPath(absRoot)
	}
	configPath = filepath.Clean(configPath)
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolving config path: %w", err)
	}
	if name == "" {
		name = filepath.Base(absRoot)
	}
	return &domain.Project{ID: id, Name: name, RootPath: absRoot, ConfigPath: absConfig}, nil
}

func validateProjectRegistration(project *domain.Project) error {
	if info, err := os.Stat(project.RootPath); err != nil || !info.IsDir() {
		return fmt.Errorf("project root does not exist: %s", project.RootPath)
	}
	if _, err := os.Stat(project.ConfigPath); err != nil {
		return fmt.Errorf("project config not found: %s", project.ConfigPath)
	}
	return nil
}

func decodeJSON(r *http.Request, dst any) error {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	return nil
}
