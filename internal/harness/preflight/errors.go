package preflight

import (
	"fmt"
	"strings"
)

type Problem struct {
	Requirement string
	Summary     string
	Fix         string
}

type Failure struct {
	BlueprintID string
	Problems    []Problem
}

func (f Failure) Error() string {
	var b strings.Builder
	if f.BlueprintID != "" {
		b.WriteString(fmt.Sprintf("preflight failed for blueprint %q:", f.BlueprintID))
	} else {
		b.WriteString("preflight failed:")
	}
	for _, p := range f.Problems {
		b.WriteString("\n- ")
		if p.Requirement != "" {
			b.WriteString(p.Requirement)
			b.WriteString(": ")
		}
		b.WriteString(p.Summary)
		if p.Fix != "" {
			b.WriteString("\n  fix: ")
			b.WriteString(p.Fix)
		}
	}
	return b.String()
}

func (f Failure) Is(target error) bool {
	_, ok := target.(Failure)
	return ok
}
