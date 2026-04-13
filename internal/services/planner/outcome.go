package planner

import (
	"fmt"
	"strings"

	"github.com/syndg/tack/internal/domain"
)

const (
	plannerOutcomePrefix   = "PLANNER_OUTCOME:"
	plannerReasonPrefix    = "PLANNER_REASON:"
	plannerFocusPrefix     = "PLANNER_FOCUS_AREAS:"
	plannerFileHintsPrefix = "PLANNER_FILE_HINTS:"
	plannerQuestionsPrefix = "PLANNER_QUESTIONS:"

	plannerOutcomeNeedsDossierExpansion = "needs_dossier_expansion"
)

type NeedsDossierExpansionError struct {
	Request domain.DossierExpansionRequest
}

func (e *NeedsDossierExpansionError) Error() string {
	reason := strings.TrimSpace(e.Request.Reason)
	if reason == "" {
		reason = "planner requested dossier expansion"
	}
	return fmt.Sprintf("planner requested dossier expansion: %s", reason)
}

func ParseDossierExpansionRequest(summary string) (domain.DossierExpansionRequest, bool) {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return domain.DossierExpansionRequest{}, false
	}

	lines := strings.Split(trimmed, "\n")
	outcome := ""
	request := domain.DossierExpansionRequest{}
	section := ""

	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, plannerOutcomePrefix):
			outcome = strings.ToLower(strings.TrimSpace(line[len(plannerOutcomePrefix):]))
			section = ""
		case strings.HasPrefix(upper, plannerReasonPrefix):
			request.Reason = strings.TrimSpace(line[len(plannerReasonPrefix):])
			section = ""
		case strings.HasPrefix(upper, plannerFocusPrefix):
			section = "focus"
			appendPlannerListItem(&request.FocusAreas, strings.TrimSpace(line[len(plannerFocusPrefix):]))
		case strings.HasPrefix(upper, plannerFileHintsPrefix):
			section = "files"
			appendPlannerListItem(&request.FileHints, strings.TrimSpace(line[len(plannerFileHintsPrefix):]))
		case strings.HasPrefix(upper, plannerQuestionsPrefix):
			section = "questions"
			appendPlannerListItem(&request.Questions, strings.TrimSpace(line[len(plannerQuestionsPrefix):]))
		case strings.HasPrefix(line, "- "):
			item := strings.TrimSpace(line[2:])
			switch section {
			case "focus":
				appendPlannerListItem(&request.FocusAreas, item)
			case "files":
				appendPlannerListItem(&request.FileHints, item)
			case "questions":
				appendPlannerListItem(&request.Questions, item)
			}
		}
	}

	if outcome != plannerOutcomeNeedsDossierExpansion {
		return domain.DossierExpansionRequest{}, false
	}
	if strings.TrimSpace(request.Reason) == "" {
		request.Reason = "planner could not decompose safely from the current dossier"
	}
	return request, true
}

func appendPlannerListItem(target *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *target {
		if existing == value {
			return
		}
	}
	*target = append(*target, value)
}
