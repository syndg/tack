package local

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copyIgnoredFiles copies gitignored files from the main repo to a worktree.
// This eliminates cold starts by sharing node_modules, build caches, etc.
//
// Behavior:
//   - If .worktreeinclude exists in the repo root, only files matching those
//     patterns (AND gitignored) are copied. Otherwise all gitignored files are copied.
//   - Uses reflink (copy-on-write) where available via cp -c.
//   - Skips .git, existing files in destination, and files outside the repo.
func copyIgnoredFiles(repoRoot, worktreePath string, logger *slog.Logger) error {
	// Get list of gitignored files in the repo root
	ignoredFiles, err := listIgnoredFiles(repoRoot)
	if err != nil {
		return fmt.Errorf("listing ignored files: %w", err)
	}

	if len(ignoredFiles) == 0 {
		return nil
	}

	// Load .worktreeinclude patterns if they exist
	includePatterns := loadWorktreeInclude(repoRoot)

	copied := 0
	skipped := 0
	for _, relPath := range ignoredFiles {
		// Skip .git and VCS metadata
		if strings.HasPrefix(relPath, ".git/") || strings.HasPrefix(relPath, ".git") {
			continue
		}

		// If .worktreeinclude exists, filter by it
		if len(includePatterns) > 0 && !matchesInclude(relPath, includePatterns) {
			continue
		}

		src := filepath.Join(repoRoot, relPath)
		dst := filepath.Join(worktreePath, relPath)

		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			skipped++
			continue
		}

		// Create parent directory
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			logger.Warn("failed to create parent dir for copy-ignored", "path", dst, "error", err)
			continue
		}

		// Copy with reflink if possible, fall back to regular copy
		if err := copyFileReflink(src, dst); err != nil {
			logger.Debug("copy-ignored file failed", "src", relPath, "error", err)
			continue
		}
		copied++
	}

	if copied > 0 {
		logger.Info("copied gitignored files to worktree",
			"copied", copied,
			"skipped", skipped,
			"worktree", filepath.Base(worktreePath),
		)
	}

	return nil
}

// listIgnoredFiles returns relative paths of all gitignored files in the repo.
func listIgnoredFiles(repoRoot string) ([]string, error) {
	// git ls-files --others --ignored --exclude-standard --directory
	// Using --directory to get directory names (like node_modules/) instead of
	// listing every file inside them.
	cmd := exec.Command("git", "ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// loadWorktreeInclude reads .worktreeinclude from the repo root.
// Returns nil if the file doesn't exist.
func loadWorktreeInclude(repoRoot string) []string {
	path := filepath.Join(repoRoot, ".worktreeinclude")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchesInclude checks if a path matches any .worktreeinclude pattern.
func matchesInclude(path string, patterns []string) bool {
	for _, pattern := range patterns {
		// Directory pattern: "node_modules/" matches "node_modules/..."
		if strings.HasSuffix(pattern, "/") {
			dir := strings.TrimSuffix(pattern, "/")
			if path == dir || strings.HasPrefix(path, dir+"/") || path == dir+"/" {
				return true
			}
			continue
		}
		// Exact match
		if path == pattern || strings.HasPrefix(path, pattern+"/") {
			return true
		}
		// Glob match
		if matched, _ := filepath.Match(pattern, path); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			return true
		}
	}
	return false
}

// copyFileReflink copies a file or directory using cp -c (reflink/CoW) on macOS,
// falling back to cp -R on other systems or if reflink fails.
func copyFileReflink(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	// For directories, use cp -Rc (recursive with reflink)
	if info.IsDir() {
		cmd := exec.Command("cp", "-Rc", src, dst)
		if out, err := cmd.CombinedOutput(); err != nil {
			// Reflink not supported, fall back to regular copy
			cmd2 := exec.Command("cp", "-R", src, dst)
			if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
				return fmt.Errorf("cp -R: %w (%s)", err2, string(out2))
			}
			_ = out
		}
		return nil
	}

	// For symlinks, recreate the link
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}

	// For regular files, try reflink first
	cmd := exec.Command("cp", "-c", src, dst)
	if err := cmd.Run(); err != nil {
		// Fall back to regular file copy
		return copyFileRegular(src, dst, info.Mode())
	}
	return nil
}

// copyFileRegular copies a file using standard I/O.
func copyFileRegular(src, dst string, mode os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer df.Close()

	_, err = io.Copy(df, sf)
	return err
}
