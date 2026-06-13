package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestGitFilesToBackupIncludesLocalChangesOnly(t *testing.T) {
	rootDir := t.TempDir()
	remoteDir := filepath.Join(rootDir, "remote.git")
	projectDir := filepath.Join(rootDir, "project")
	otherDir := filepath.Join(rootDir, "other")

	git(t, rootDir, "init", "--bare", remoteDir)
	git(t, rootDir, "init", "-b", "main", projectDir)
	configureGitUser(t, projectDir)

	writeFile(t, projectDir, "base.txt", "base\n")
	git(t, projectDir, "add", "base.txt")
	git(t, projectDir, "commit", "-m", "base")
	git(t, projectDir, "remote", "add", "origin", remoteDir)
	git(t, projectDir, "push", "-u", "origin", "main")

	git(t, rootDir, "clone", remoteDir, otherDir)
	configureGitUser(t, otherDir)
	writeFile(t, otherDir, "remote-only.txt", "remote\n")
	git(t, otherDir, "add", "remote-only.txt")
	git(t, otherDir, "commit", "-m", "remote only")
	git(t, otherDir, "push", "origin", "main")

	writeFile(t, projectDir, "local-commit.txt", "local commit\n")
	git(t, projectDir, "add", "local-commit.txt")
	git(t, projectDir, "commit", "-m", "local commit")

	writeFile(t, projectDir, "base.txt", "local working tree change\n")
	writeFile(t, projectDir, "staged.txt", "staged\n")
	git(t, projectDir, "add", "staged.txt")
	writeFile(t, projectDir, "untracked.txt", "untracked\n")
	git(t, projectDir, "fetch", "origin")

	files, err := gitFilesToBackup(projectDir, "origin")
	if err != nil {
		t.Fatalf("gitFilesToBackup returned error: %v", err)
	}

	expectedFiles := []string{
		"base.txt",
		"local-commit.txt",
		"staged.txt",
		"untracked.txt",
	}
	if !reflect.DeepEqual(files, expectedFiles) {
		t.Fatalf("files = %#v, want %#v", files, expectedFiles)
	}
}

func TestGitFilesToBackupStillIncludesWorkingChangesWithoutRemote(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "project")

	git(t, "", "init", "-b", "main", projectDir)
	configureGitUser(t, projectDir)

	writeFile(t, projectDir, "tracked.txt", "tracked\n")
	git(t, projectDir, "add", "tracked.txt")
	git(t, projectDir, "commit", "-m", "base")

	writeFile(t, projectDir, "tracked.txt", "changed\n")
	writeFile(t, projectDir, "staged.txt", "staged\n")
	git(t, projectDir, "add", "staged.txt")
	writeFile(t, projectDir, "untracked.txt", "untracked\n")

	files, err := gitFilesToBackup(projectDir, "origin")
	if err != nil {
		t.Fatalf("gitFilesToBackup returned error: %v", err)
	}

	expectedFiles := []string{
		"staged.txt",
		"tracked.txt",
		"untracked.txt",
	}
	if !reflect.DeepEqual(files, expectedFiles) {
		t.Fatalf("files = %#v, want %#v", files, expectedFiles)
	}
}

func TestNormalizeForceIncludeRejectsEscapes(t *testing.T) {
	rejectedPaths := []string{
		"",
		".",
		"..",
		"../secret",
	}

	if runtime.GOOS == "windows" {
		rejectedPaths = append(rejectedPaths, `C:\secret`)
	} else {
		rejectedPaths = append(rejectedPaths, "/secret")
	}

	for _, path := range rejectedPaths {
		t.Run(path, func(t *testing.T) {
			if _, err := normalizeForceInclude(path); err == nil {
				t.Fatalf("normalizeForceInclude(%q) returned nil error", path)
			}
		})
	}

	normalizedPath, err := normalizeForceInclude("dir/file.txt")
	if err != nil {
		t.Fatalf("normalizeForceInclude returned error: %v", err)
	}

	if normalizedPath != filepath.Join("dir", "file.txt") {
		t.Fatalf("normalizedPath = %q", normalizedPath)
	}
}

func TestValidateConfiguredPathsRejectsOverlappingDirectories(t *testing.T) {
	rootDir := t.TempDir()
	projectsDir := filepath.Join(rootDir, "projects")
	backupDir := filepath.Join(rootDir, "backup")
	mkdirAll(t, projectsDir)
	mkdirAll(t, backupDir)

	if err := validateConfiguredPaths(projectsDir, backupDir); err != nil {
		t.Fatalf("validateConfiguredPaths returned error for sibling dirs: %v", err)
	}

	overlappingCases := map[string]string{
		"same":                 projectsDir,
		"backup inside source": filepath.Join(projectsDir, "backup"),
		"source inside backup": rootDir,
	}

	for name, candidateBackupDir := range overlappingCases {
		t.Run(name, func(t *testing.T) {
			if err := validateConfiguredPaths(projectsDir, candidateBackupDir); err == nil {
				t.Fatalf("validateConfiguredPaths(%q, %q) returned nil error", projectsDir, candidateBackupDir)
			}
		})
	}
}

func configureGitUser(t *testing.T, dir string) {
	t.Helper()

	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test User")
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	path := filepath.Join(dir, name)
	mkdirAll(t, filepath.Dir(path))

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("failed to create %s: %v", path, err)
	}
}
