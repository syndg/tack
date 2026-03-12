package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/syndg/deck/internal/sandbox"
)

func TestParseDiffStat_Standard(t *testing.T) {
	output := ` src/auth/token.ts | 42 +++++++++++++++++++++++++++++++-----------
 src/auth/jwt.ts   | 14 ++++++++++++++
 src/config.ts     |  8 --------
 3 files changed, 48 insertions(+), 19 deletions(-)`

	files, totalIns, totalDel := ParseDiffStat(output)

	if totalIns != 48 {
		t.Errorf("totalInsertions = %d, want 48", totalIns)
	}
	if totalDel != 19 {
		t.Errorf("totalDeletions = %d, want 19", totalDel)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3", len(files))
	}
	if files[0].Path != "src/auth/token.ts" {
		t.Errorf("files[0].Path = %q, want %q", files[0].Path, "src/auth/token.ts")
	}
}

func TestParseDiffStat_InsertionsOnly(t *testing.T) {
	output := ` new_file.go | 25 +++++++++++++++++++++++++
 1 file changed, 25 insertions(+)`

	files, totalIns, totalDel := ParseDiffStat(output)

	if totalIns != 25 {
		t.Errorf("totalInsertions = %d, want 25", totalIns)
	}
	if totalDel != 0 {
		t.Errorf("totalDeletions = %d, want 0", totalDel)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
}

func TestParseDiffStat_DeletionsOnly(t *testing.T) {
	output := ` removed.go | 10 ----------
 1 file changed, 10 deletions(-)`

	_, totalIns, totalDel := ParseDiffStat(output)

	if totalIns != 0 {
		t.Errorf("totalInsertions = %d, want 0", totalIns)
	}
	if totalDel != 10 {
		t.Errorf("totalDeletions = %d, want 10", totalDel)
	}
}

func TestParseDiffStat_BinaryFile(t *testing.T) {
	output := ` image.png | Bin 0 -> 12345 bytes
 1 file changed, 0 insertions(+), 0 deletions(-)`

	files, _, _ := ParseDiffStat(output)

	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	if files[0].Path != "image.png" {
		t.Errorf("files[0].Path = %q, want %q", files[0].Path, "image.png")
	}
	// Binary file has no +/- counts.
	if files[0].Insertions != 0 {
		t.Errorf("insertions = %d, want 0", files[0].Insertions)
	}
}

func TestParseDiffStat_Empty(t *testing.T) {
	files, totalIns, totalDel := ParseDiffStat("")

	if files != nil {
		t.Errorf("expected nil files for empty output, got %v", files)
	}
	if totalIns != 0 {
		t.Errorf("totalInsertions = %d, want 0", totalIns)
	}
	if totalDel != 0 {
		t.Errorf("totalDeletions = %d, want 0", totalDel)
	}
}

func TestParseNameStatus_Standard(t *testing.T) {
	output := "M\tsrc/auth/token.ts\nA\tsrc/auth/jwt.ts\nD\tsrc/old.ts"

	result := ParseNameStatus(output)

	if len(result) != 3 {
		t.Fatalf("len = %d, want 3", len(result))
	}
	if result["src/auth/token.ts"] != "modified" {
		t.Errorf("token.ts status = %q, want modified", result["src/auth/token.ts"])
	}
	if result["src/auth/jwt.ts"] != "added" {
		t.Errorf("jwt.ts status = %q, want added", result["src/auth/jwt.ts"])
	}
	if result["src/old.ts"] != "deleted" {
		t.Errorf("old.ts status = %q, want deleted", result["src/old.ts"])
	}
}

func TestParseNameStatus_Rename(t *testing.T) {
	output := "R100\tsrc/old_name.ts\tsrc/new_name.ts"

	result := ParseNameStatus(output)

	if len(result) != 1 {
		t.Fatalf("len = %d, want 1", len(result))
	}
	if result["src/new_name.ts"] != "renamed" {
		t.Errorf("new_name.ts status = %q, want renamed", result["src/new_name.ts"])
	}
	// Old name should not be in the result.
	if _, ok := result["src/old_name.ts"]; ok {
		t.Error("old_name.ts should not be present (renamed to new_name.ts)")
	}
}

func TestParseNameStatus_Empty(t *testing.T) {
	result := ParseNameStatus("")

	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestParseNameStatus_AllCodes(t *testing.T) {
	output := "A\tadded.go\nM\tmodified.go\nD\tdeleted.go\nC100\tsrc.go\tcopy.go\nT\ttype_changed.go"

	result := ParseNameStatus(output)

	// Note: Copy entries (C100) use parts[1] (source path) because only R-prefixed
	// entries get the destination-path redirect in the current implementation.
	expected := map[string]string{
		"added.go":        "added",
		"modified.go":     "modified",
		"deleted.go":      "deleted",
		"src.go":          "copied",
		"type_changed.go": "type_changed",
	}
	for path, wantStatus := range expected {
		if got := result[path]; got != wantStatus {
			t.Errorf("status[%q] = %q, want %q", path, got, wantStatus)
		}
	}
}

func TestExtract_MockSandbox(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch cmd {
			case "git diff --stat main...feature":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   " src/auth.go | 10 ++++++----\n 1 file changed, 6 insertions(+), 4 deletions(-)\n",
				}, nil
			case "git diff --name-status main...feature":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   "M\tsrc/auth.go\n",
				}, nil
			case "git diff main...feature":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   "diff --git a/src/auth.go b/src/auth.go\n--- a/src/auth.go\n+++ b/src/auth.go\n@@ -1,3 +1,5 @@\n+// new code\n",
				}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	extractor := NewDiffExtractor(slog.Default())
	summary, err := extractor.Extract(context.Background(), sb, "main", "feature")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if summary.FilesChanged != 1 {
		t.Errorf("FilesChanged = %d, want 1", summary.FilesChanged)
	}
	if summary.Insertions != 6 {
		t.Errorf("Insertions = %d, want 6", summary.Insertions)
	}
	if summary.Deletions != 4 {
		t.Errorf("Deletions = %d, want 4", summary.Deletions)
	}
	if len(summary.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(summary.Files))
	}
	if summary.Files[0].Path != "src/auth.go" {
		t.Errorf("Files[0].Path = %q, want %q", summary.Files[0].Path, "src/auth.go")
	}
	if summary.Files[0].Status != "modified" {
		t.Errorf("Files[0].Status = %q, want %q", summary.Files[0].Status, "modified")
	}
	if summary.Files[0].Patch == "" {
		t.Error("expected non-empty patch content")
	}
}

func TestExtractJSON_ValidJSON(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, cmd string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			switch cmd {
			case "git diff --stat base...head":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   " f.go | 2 ++\n 1 file changed, 2 insertions(+)\n",
				}, nil
			case "git diff --name-status base...head":
				return sandbox.ExecResult{
					ExitCode: 0,
					Stdout:   "A\tf.go\n",
				}, nil
			case "git diff base...head":
				return sandbox.ExecResult{ExitCode: 0, Stdout: ""}, nil
			default:
				return sandbox.ExecResult{ExitCode: 0}, nil
			}
		},
	}

	extractor := NewDiffExtractor(slog.Default())
	jsonStr, err := extractor.ExtractJSON(context.Background(), sb, "base", "head")
	if err != nil {
		t.Fatalf("ExtractJSON: %v", err)
	}

	// Verify it's valid JSON.
	var parsed DiffSummary
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}
	if parsed.FilesChanged != 1 {
		t.Errorf("parsed.FilesChanged = %d, want 1", parsed.FilesChanged)
	}
	if parsed.Insertions != 2 {
		t.Errorf("parsed.Insertions = %d, want 2", parsed.Insertions)
	}
}

func TestExtract_SandboxError(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{}, fmt.Errorf("sandbox unavailable")
		},
	}

	extractor := NewDiffExtractor(slog.Default())
	_, err := extractor.Extract(context.Background(), sb, "main", "head")
	if err == nil {
		t.Fatal("expected error from sandbox failure")
	}
}

func TestExtract_NonZeroExitCode(t *testing.T) {
	sb := &mockSandbox{
		id: "test-sb",
		execFn: func(_ context.Context, _ string, _ sandbox.ExecOpts) (sandbox.ExecResult, error) {
			return sandbox.ExecResult{ExitCode: 128, Stderr: "fatal: bad revision"}, nil
		},
	}

	extractor := NewDiffExtractor(slog.Default())
	_, err := extractor.Extract(context.Background(), sb, "bad-ref", "head")
	if err == nil {
		t.Fatal("expected error from non-zero exit code")
	}
}
