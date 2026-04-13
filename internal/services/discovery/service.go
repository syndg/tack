package discovery

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/syndg/tack/internal/db"
	"github.com/syndg/tack/internal/domain"
	"github.com/syndg/tack/internal/harness/blueprint"
	"github.com/syndg/tack/internal/harness/rules"
)

type Service struct {
	projectRoot string
	objectives  *db.ObjectiveStore
	dossiers    *db.DossierStore
	rules       *rules.Engine
	blueprints  *blueprint.Registry
	logger      *slog.Logger
}

const noStrongMatchesUnknown = "No strong file matches were found from deterministic objective keyword retrieval."

func New(projectRoot string, objectives *db.ObjectiveStore, dossiers *db.DossierStore, rulesEngine *rules.Engine, blueprints *blueprint.Registry, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		projectRoot: projectRoot,
		objectives:  objectives,
		dossiers:    dossiers,
		rules:       rulesEngine,
		blueprints:  blueprints,
		logger:      logger.With("component", "discovery"),
	}
}

func (s *Service) EnsureDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error) {
	if dossier, err := s.dossiers.GetByObjective(ctx, objectiveID); err == nil {
		return dossier, nil
	}
	return s.Generate(ctx, objectiveID)
}

func (s *Service) GetDossier(ctx context.Context, objectiveID string) (*domain.Dossier, error) {
	return s.dossiers.GetByObjective(ctx, objectiveID)
}

func (s *Service) UpdateDossier(ctx context.Context, objectiveID string, dossier domain.Dossier) (*domain.Dossier, error) {
	obj, err := s.objectives.Get(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("loading objective for dossier update: %w", err)
	}
	dossier.ObjectiveID = objectiveID
	dossier.ProjectID = obj.ProjectID
	if err := s.dossiers.Upsert(ctx, &dossier); err != nil {
		return nil, err
	}
	return s.dossiers.GetByObjective(ctx, objectiveID)
}

func (s *Service) ExpandDossier(ctx context.Context, objectiveID string, request domain.DossierExpansionRequest) (*domain.Dossier, error) {
	dossier, err := s.EnsureDossier(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("ensuring dossier before expansion: %w", err)
	}
	obj, err := s.objectives.Get(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("loading objective for dossier expansion: %w", err)
	}

	expanded := *dossier
	expanded.RelevantFiles = cloneReferences(dossier.RelevantFiles)
	expanded.SimilarPatterns = cloneReferences(dossier.SimilarPatterns)
	expanded.Risks = append([]string(nil), dossier.Risks...)
	expanded.Unknowns = append([]string(nil), dossier.Unknowns...)
	expanded.SuggestedSeams = cloneSeams(dossier.SuggestedSeams)
	expanded.Citations = cloneCitations(dossier.Citations)

	queries := []string{obj.Description, request.Reason}
	queries = append(queries, request.FocusAreas...)
	queries = append(queries, request.FileHints...)
	queries = append(queries, request.Questions...)
	prefix := fmt.Sprintf("file_expand_%d", len(expanded.Citations)+1)
	additionalFiles, additionalCitations := s.discoverRelevantFiles(prefix, queries...)
	expanded.RelevantFiles = mergeReferences(expanded.RelevantFiles, additionalFiles)
	expanded.Citations = mergeCitations(expanded.Citations, additionalCitations)
	expanded.SimilarPatterns = deriveSimilarPatterns(expanded.RelevantFiles)
	expanded.SuggestedSeams = deriveSuggestedSeams(expanded.RelevantFiles)
	expanded.Unknowns = removeString(expanded.Unknowns, noStrongMatchesUnknown)
	if len(expanded.RelevantFiles) == 0 {
		expanded.Unknowns = appendUniqueStrings(expanded.Unknowns, noStrongMatchesUnknown)
	}
	expanded.Unknowns = appendUniqueStrings(expanded.Unknowns, request.Questions...)
	if len(additionalFiles) == 0 && strings.TrimSpace(request.Reason) != "" {
		expanded.Risks = appendUniqueStrings(expanded.Risks, fmt.Sprintf("Planner-requested dossier expansion found no new deterministic matches for: %s", strings.TrimSpace(request.Reason)))
	}
	expanded.Summary = buildSummary(obj.Description, expanded.BlueprintID, len(expanded.RepoPriors), expanded.RelevantFiles, expanded.SuggestedSeams)

	if err := s.dossiers.Upsert(ctx, &expanded); err != nil {
		return nil, err
	}
	s.logger.Info("dossier expanded", "objective_id", objectiveID, "new_relevant_files", len(additionalFiles), "focus_areas", len(request.FocusAreas))
	return s.dossiers.GetByObjective(ctx, objectiveID)
}

func (s *Service) Generate(ctx context.Context, objectiveID string) (*domain.Dossier, error) {
	obj, err := s.objectives.Get(ctx, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("loading objective for dossier generation: %w", err)
	}
	dossier := domain.Dossier{ObjectiveID: obj.ID, ProjectID: obj.ProjectID}
	blueprintID, blueprintName, blueprintCitation := s.resolveBlueprint(obj)
	dossier.BlueprintID = blueprintID
	if blueprintCitation.ID != "" {
		dossier.Citations = append(dossier.Citations, blueprintCitation)
		dossier.RepoPriors = append(dossier.RepoPriors, domain.DossierPrior{
			Kind:        "blueprint",
			Title:       blueprintName,
			Detail:      fmt.Sprintf("Objective will execute with blueprint %q.", blueprintID),
			CitationIDs: []string{blueprintCitation.ID},
		})
	}
	rulePriors, ruleCitations := compileRulePriors(s.rules)
	dossier.RepoPriors = append(dossier.RepoPriors, rulePriors...)
	dossier.Citations = append(dossier.Citations, ruleCitations...)
	relevantFiles, relevantCitations := s.discoverRelevantFiles("file", obj.Description)
	dossier.RelevantFiles = relevantFiles
	dossier.Citations = append(dossier.Citations, relevantCitations...)
	dossier.SimilarPatterns = deriveSimilarPatterns(relevantFiles)
	dossier.SuggestedSeams = deriveSuggestedSeams(relevantFiles)
	if len(relevantFiles) == 0 {
		dossier.Unknowns = append(dossier.Unknowns, noStrongMatchesUnknown)
	}
	if len(rulePriors) == 0 {
		dossier.Unknowns = append(dossier.Unknowns, "No explicit repo rules were loaded for this project context.")
	}
	if obj.Blueprint == "" {
		dossier.Risks = append(dossier.Risks, "Objective did not specify a blueprint; dossier used the resolved default workflow.")
	}
	dossier.Summary = buildSummary(obj.Description, blueprintID, len(rulePriors), dossier.RelevantFiles, dossier.SuggestedSeams)
	if err := s.dossiers.Upsert(ctx, &dossier); err != nil {
		return nil, err
	}
	s.logger.Info("dossier generated", "objective_id", objectiveID, "relevant_files", len(dossier.RelevantFiles), "repo_priors", len(dossier.RepoPriors))
	return s.dossiers.GetByObjective(ctx, objectiveID)
}

func (s *Service) resolveBlueprint(obj *domain.Objective) (id, name string, citation domain.DossierCitation) {
	id = strings.TrimSpace(obj.Blueprint)
	var bp *blueprint.Blueprint
	var ok bool
	if id != "" {
		bp, ok = s.blueprints.Get(id)
	} else {
		bp, _ = s.blueprints.ResolveDefault()
		if bp != nil {
			id = bp.ID
			ok = true
		}
	}
	if !ok || bp == nil {
		return id, id, domain.DossierCitation{}
	}
	name = strings.TrimSpace(bp.Name)
	if name == "" {
		name = bp.ID
	}
	return id, name, domain.DossierCitation{
		ID:     "blueprint:" + bp.ID,
		Kind:   "blueprint",
		Target: bp.ID,
		Detail: firstNonEmpty(bp.Description, fmt.Sprintf("Workflow %s", bp.ID)),
	}
}

func compileRulePriors(engine *rules.Engine) ([]domain.DossierPrior, []domain.DossierCitation) {
	if engine == nil {
		return nil, nil
	}
	loaded := engine.All()
	if len(loaded) == 0 {
		return nil, nil
	}
	priors := make([]domain.DossierPrior, 0, len(loaded))
	citations := make([]domain.DossierCitation, 0, len(loaded))
	for idx, rule := range loaded {
		if rule == nil {
			continue
		}
		id := fmt.Sprintf("rule:%d", idx+1)
		citations = append(citations, domain.DossierCitation{
			ID:     id,
			Kind:   "rule",
			Target: rule.Source,
			Detail: fmt.Sprintf("scope=%s priority=%s", rule.Scope, rule.Priority),
		})
		priors = append(priors, domain.DossierPrior{
			Kind:        "rule",
			Title:       filepath.Base(rule.Source),
			Detail:      firstNonEmpty(rule.Body, fmt.Sprintf("Rule scoped to %s.", rule.Scope)),
			CitationIDs: []string{id},
		})
	}
	return priors, citations
}

func (s *Service) discoverRelevantFiles(citationPrefix string, texts ...string) ([]domain.DossierReference, []domain.DossierCitation) {
	tokens := objectiveTokens(texts...)
	if len(tokens) == 0 || strings.TrimSpace(s.projectRoot) == "" {
		return nil, nil
	}
	type scoredFile struct {
		path   string
		score  int
		reason string
	}
	var matches []scoredFile
	_ = filepath.WalkDir(s.projectRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(s.projectRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if shouldSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(rel) {
			return nil
		}
		score, reason := scoreFile(path, rel, tokens)
		if score > 0 {
			matches = append(matches, scoredFile{path: rel, score: score, reason: reason})
		}
		return nil
	})
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > 8 {
		matches = matches[:8]
	}
	refs := make([]domain.DossierReference, 0, len(matches))
	citations := make([]domain.DossierCitation, 0, len(matches))
	for i, match := range matches {
		cid := fmt.Sprintf("%s:%d", citationPrefix, i+1)
		refs = append(refs, domain.DossierReference{Path: match.path, Reason: match.reason, CitationIDs: []string{cid}})
		citations = append(citations, domain.DossierCitation{ID: cid, Kind: "file", Target: match.path, Detail: match.reason})
	}
	return refs, citations
}

func deriveSimilarPatterns(files []domain.DossierReference) []domain.DossierReference {
	if len(files) == 0 {
		return nil
	}
	limit := 3
	if len(files) < limit {
		limit = len(files)
	}
	return append([]domain.DossierReference(nil), files[:limit]...)
}

func deriveSuggestedSeams(files []domain.DossierReference) []domain.DossierSeam {
	if len(files) == 0 {
		return nil
	}
	type seamGroup struct {
		title string
		files []string
		ids   []string
	}
	groups := map[string]*seamGroup{}
	for _, file := range files {
		title := seamTitleForPath(file.Path)
		group := groups[title]
		if group == nil {
			group = &seamGroup{title: title}
			groups[title] = group
		}
		group.files = append(group.files, file.Path)
		group.ids = append(group.ids, file.CitationIDs...)
	}
	seams := make([]domain.DossierSeam, 0, len(groups))
	for _, group := range groups {
		seams = append(seams, domain.DossierSeam{
			Title:       group.title,
			Reason:      fmt.Sprintf("Relevant files cluster under %s.", group.title),
			FilePaths:   append([]string(nil), group.files...),
			CitationIDs: append([]string(nil), group.ids...),
		})
	}
	sort.Slice(seams, func(i, j int) bool {
		if len(seams[i].FilePaths) != len(seams[j].FilePaths) {
			return len(seams[i].FilePaths) > len(seams[j].FilePaths)
		}
		return seams[i].Title < seams[j].Title
	})
	if len(seams) > 4 {
		seams = seams[:4]
	}
	return seams
}

func buildSummary(description, blueprintID string, priorCount int, files []domain.DossierReference, seams []domain.DossierSeam) string {
	parts := []string{fmt.Sprintf("Objective: %s.", strings.TrimSpace(description))}
	if strings.TrimSpace(blueprintID) != "" {
		parts = append(parts, fmt.Sprintf("Discovery resolved blueprint %q.", blueprintID))
	}
	parts = append(parts, fmt.Sprintf("Captured %d repo priors and %d likely relevant files.", priorCount, len(files)))
	if len(seams) > 0 {
		names := make([]string, 0, len(seams))
		for _, seam := range seams {
			names = append(names, seam.Title)
		}
		parts = append(parts, fmt.Sprintf("Suggested seams: %s.", strings.Join(names, ", ")))
	}
	return strings.Join(parts, " ")
}

func objectiveTokens(texts ...string) []string {
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", ".", " ", ",", " ", ":", " ", "(", " ", ")", " ")
	stop := map[string]struct{}{"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "into": {}, "that": {}, "this": {}, "using": {}, "add": {}, "support": {}, "make": {}, "work": {}, "flow": {}, "flows": {}, "basic": {}, "recent": {}, "plain": {}, "focused": {}}
	seen := map[string]struct{}{}
	var out []string
	for _, text := range texts {
		normalized := strings.ToLower(replacer.Replace(text))
		for _, token := range strings.Fields(normalized) {
			if len(token) < 3 {
				continue
			}
			if _, ok := stop[token]; ok {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			out = append(out, token)
		}
	}
	return out
}

func shouldSkipDir(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		switch part {
		case ".git", ".jj", "node_modules", "vendor", "tmp", "dist", "build":
			return true
		}
	}
	return false
}

func shouldSkipFile(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".pdf", ".zip", ".db", ".sqlite", ".bin":
		return true
	}
	return strings.HasPrefix(rel, ".git/")
}

func scoreFile(absPath, rel string, tokens []string) (int, string) {
	base := strings.ToLower(filepath.Base(rel))
	relLower := strings.ToLower(rel)
	score := 0
	reasons := []string{}
	for _, token := range tokens {
		if strings.Contains(base, token) {
			score += 8
			reasons = append(reasons, fmt.Sprintf("filename matches %q", token))
			continue
		}
		if strings.Contains(relLower, token) {
			score += 4
			reasons = append(reasons, fmt.Sprintf("path matches %q", token))
		}
	}
	if score == 0 {
		return 0, ""
	}
	if info, err := os.Stat(absPath); err == nil && info.Size() > 0 && info.Size() <= 256*1024 {
		if content, err := os.ReadFile(absPath); err == nil {
			text := strings.ToLower(string(content))
			for _, token := range tokens {
				if strings.Contains(text, token) {
					score += 2
					reasons = append(reasons, fmt.Sprintf("content mentions %q", token))
				}
			}
		}
	}
	return score, dedupeReasons(reasons)
}

func dedupeReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		out = append(out, reason)
	}
	return strings.Join(out, "; ")
}

func seamTitleForPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	if dir := filepath.ToSlash(filepath.Dir(path)); dir != "." {
		return dir
	}
	return path
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendUniqueStrings(existing []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, current := range existing {
			if current == value {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, value)
		}
	}
	return existing
}

func removeString(values []string, target string) []string {
	target = strings.TrimSpace(target)
	if target == "" {
		return values
	}
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			continue
		}
		out = append(out, value)
	}
	return out
}

func mergeReferences(existing, incoming []domain.DossierReference) []domain.DossierReference {
	if len(incoming) == 0 {
		return existing
	}
	byPath := make(map[string]int, len(existing))
	for i, ref := range existing {
		byPath[ref.Path] = i
	}
	for _, ref := range incoming {
		if idx, ok := byPath[ref.Path]; ok {
			existing[idx].Reason = dedupeReasons([]string{existing[idx].Reason, ref.Reason})
			existing[idx].CitationIDs = appendUniqueStrings(existing[idx].CitationIDs, ref.CitationIDs...)
			continue
		}
		byPath[ref.Path] = len(existing)
		existing = append(existing, ref)
	}
	return existing
}

func mergeCitations(existing, incoming []domain.DossierCitation) []domain.DossierCitation {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing))
	for _, citation := range existing {
		seen[citation.ID] = struct{}{}
	}
	for _, citation := range incoming {
		if _, ok := seen[citation.ID]; ok {
			continue
		}
		seen[citation.ID] = struct{}{}
		existing = append(existing, citation)
	}
	return existing
}

func cloneReferences(values []domain.DossierReference) []domain.DossierReference {
	out := make([]domain.DossierReference, len(values))
	copy(out, values)
	return out
}

func cloneSeams(values []domain.DossierSeam) []domain.DossierSeam {
	out := make([]domain.DossierSeam, len(values))
	copy(out, values)
	return out
}

func cloneCitations(values []domain.DossierCitation) []domain.DossierCitation {
	out := make([]domain.DossierCitation, len(values))
	copy(out, values)
	return out
}
