package gates

import (
	"strings"
	"testing"
)

func TestExtractErrorFiles_TypeScriptErrors(t *testing.T) {
	output := `src/routes/expenses.ts(76,9): error TS2345: Argument of type 'string | undefined'
src/routes/settlements.ts(66,5): error TS2345: Argument of type 'string | undefined'`

	files := ExtractErrorFiles(output)
	want := map[string]bool{
		"src/routes/expenses.ts":    true,
		"src/routes/settlements.ts": true,
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(files), len(want), files)
	}
	for _, f := range files {
		if !want[f] {
			t.Errorf("unexpected file: %s", f)
		}
	}
}

func TestExtractErrorFiles_ESLintErrors(t *testing.T) {
	output := `frontend/src/components/ExpenseForm.tsx: 45:14  warning  setState in useEffect
frontend/src/app/layout.tsx: 46:19  error  Do not use <a>
frontend/src/app/page.tsx: 11:29  warning  unused variable`

	files := ExtractErrorFiles(output)
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3: %v", len(files), files)
	}
}

func TestExtractErrorFiles_TestRunner(t *testing.T) {
	output := `FAIL src/routes/users.test.ts > POST /users > validates input
 ✓ src/routes/groups.test.ts (5 tests passed)`

	files := ExtractErrorFiles(output)
	// Should find both test files
	found := false
	for _, f := range files {
		if f == "src/routes/users.test.ts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find src/routes/users.test.ts in %v", files)
	}
}

func TestExtractErrorFiles_Deduplicates(t *testing.T) {
	output := `src/foo.ts(1,1): error
src/foo.ts(2,2): error
src/foo.ts(3,3): error`

	files := ExtractErrorFiles(output)
	if len(files) != 1 {
		t.Errorf("got %d files, want 1 (deduped): %v", len(files), files)
	}
}

func TestExtractErrorFiles_SkipsFalsePositives(t *testing.T) {
	output := `error: something went wrong
at https://example.com/docs
see github.com/syndg/tack for details`

	files := ExtractErrorFiles(output)
	if len(files) != 0 {
		t.Errorf("got %d files, want 0 (false positives): %v", len(files), files)
	}
}

func TestFilesInScope_MatchesGlobs(t *testing.T) {
	files := []string{
		"backend/src/routes/users.ts",
		"backend/src/routes/groups.ts",
		"frontend/src/app/page.tsx",
		"backend/src/types/index.ts",
	}
	scope := []string{"backend/src/routes/**"}

	matched := FilesInScope(files, scope)
	if len(matched) != 2 {
		t.Fatalf("got %d matched, want 2: %v", len(matched), matched)
	}
}

func TestFilesInScope_EmptyScopeReturnsAll(t *testing.T) {
	files := []string{"a.ts", "b.ts"}
	matched := FilesInScope(files, nil)
	if len(matched) != 2 {
		t.Errorf("empty scope should return all files, got %d", len(matched))
	}
}

func TestFilesInScope_ExactMatch(t *testing.T) {
	files := []string{"backend/src/types/index.ts", "backend/src/routes/users.ts"}
	scope := []string{"backend/src/types/index.ts"}

	matched := FilesInScope(files, scope)
	if len(matched) != 1 || matched[0] != "backend/src/types/index.ts" {
		t.Errorf("got %v, want [backend/src/types/index.ts]", matched)
	}
}

func TestFilesInScope_WildcardPattern(t *testing.T) {
	files := []string{
		"backend/src/routes/users.ts",
		"backend/src/routes/users.test.ts",
	}
	scope := []string{"backend/src/routes/*.ts"}

	matched := FilesInScope(files, scope)
	if len(matched) != 2 {
		t.Errorf("got %d matched, want 2: %v", len(matched), matched)
	}
}

func TestFilterOutputByScope(t *testing.T) {
	output := `src/routes/expenses.ts(76,9): error TS2345: bad type
src/routes/settlements.ts(66,5): error TS2345: bad type
src/types/index.ts(10,1): error TS1005: missing semicolon`

	scope := []string{"src/routes/**"}
	filtered := FilterOutputByScope(output, scope)

	if strings.Contains(filtered, "index.ts") {
		t.Error("filtered output should not contain out-of-scope file")
	}
	if !strings.Contains(filtered, "expenses.ts") {
		t.Error("filtered output should contain in-scope file")
	}
}
