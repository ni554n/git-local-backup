package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
)

const VERSION = "1.1"

//#region Define CLI flags

type forceIncludedFiles []string

func (fileNames *forceIncludedFiles) String() string {
	return fmt.Sprintf("%s", *fileNames)
}

func (fileNames *forceIncludedFiles) Set(value string) error {
	normalizedPath, err := normalizeForceInclude(value)
	if err != nil {
		return err
	}

	*fileNames = append(*fileNames, normalizedPath)

	return nil
}

type StringSet map[string]struct{}

var (
	projectsPath          = flag.String("projects-dir", "", "Path to the projects directory (required)")
	backupPath            = flag.String("backup-dir", "", "Path to an empty backup directory (required)\nOtherwise, existing files may be removed from that directory.")
	remoteBranch          = flag.String("remote-branch", "origin", "Remote name")
	dryRun                = flag.Bool("dry-run", false, "Preview changes without modifying the backup directory")
	forceIncludedRelPaths forceIncludedFiles
)

func init() {
	flag.Var(&forceIncludedRelPaths, "force-include", "Always include a git ignored `file/directory` like \".git\".\nCan be specified multiple times to include multiple items.")

	flag.Usage = func() {
		message := `Git Local Backup v%s

A tool for copying local files from Git projects to a cloud drive or a backup disk for safekeeping.
It copies only the files that have been modified since the last backup, including:

  - Committed files that are not yet pushed to the remote repository
  - Working and staged files that are not yet committed
  - Files that are not yet tracked by "git add"
  - Any .gitignored file included via "--force-include" flag
  ... basically every unpushed file that can be lost during an incident.

Usage: %v [FLAGS] --projects-dir "<path>" --backup-dir "<path>"

> Use either - or -- for flags. They are equivalent.

Flags:

`
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, message, VERSION, filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		fmt.Fprintf(w, "\nVisit https://github.com/ni554n/git-local-backup for scheduling instructions.\n")
	}
}

//#endregion Define CLI flags

func main() {
	//#region Parse flags

	flag.Parse()

	if *projectsPath == "" || *backupPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	expandedProjectsPath, err := expandHome(*projectsPath)
	panicIf(err)
	*projectsPath = expandedProjectsPath

	expandedBackupPath, err := expandHome(*backupPath)
	panicIf(err)
	*backupPath = expandedBackupPath

	err = validateConfiguredPaths(*projectsPath, *backupPath)
	panicIf(err)

	//#endregion Parse flags

	// Check if git is installed
	_, err = exec.LookPath("git")
	panicIf(err)

	//#region Read the full backup directory

	backedUpDirRelPaths := []string{}

	backedUpFileRelPaths := make(StringSet)

	err = filepath.WalkDir(*backupPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		entryRelPath, err := filepath.Rel(*backupPath, path)
		if err != nil {
			return err
		}

		if entry.IsDir() {
			backedUpDirRelPaths = append(backedUpDirRelPaths, entryRelPath)
		} else {
			backedUpFileRelPaths[entryRelPath] = struct{}{}
		}

		return nil
	})
	panicIf(err)

	//#endregion Read the full backup directory

	//#region Visit each project directory and make a list of files to backup

	projectDirEntries, err := os.ReadDir(*projectsPath)
	panicIf(err)

	projectFiles := []string{}

	for _, projectDir := range projectDirEntries {
		if !projectDir.IsDir() {
			continue
		}

		projectDirPath := filepath.Join(*projectsPath, projectDir.Name())

		// Skip over non-git projects
		if _, err := os.Stat(filepath.Join(projectDirPath, ".git")); os.IsNotExist(err) {
			continue
		}

		includedFiles, err := gitFilesToBackup(projectDirPath, *remoteBranch)
		panicIf(err)

		for _, forceIncludedRelPath := range forceIncludedRelPaths {
			forceIncludedPath := filepath.Join(projectDirPath, forceIncludedRelPath)

			info, err := os.Stat(forceIncludedPath)
			if os.IsNotExist(err) {
				continue
			}
			panicIf(err)

			if info.IsDir() {
				err := filepath.WalkDir(forceIncludedPath, func(path string, entry fs.DirEntry, err error) error {
					if err != nil {
						return err
					}

					if !entry.IsDir() {
						entryRelPath, err := filepath.Rel(projectDirPath, path)
						panicIf(err)
						includedFiles = append(includedFiles, entryRelPath)
					}

					return nil
				})
				panicIf(err)
			} else {
				includedFiles = append(includedFiles, forceIncludedRelPath)
			}
		}

		// Add current project dir to the each element in the includedFiles
		for _, includedFile := range includedFiles {
			if strings.TrimSpace(includedFile) == "" {
				continue
			}

			projectFiles = append(projectFiles, filepath.Join(projectDir.Name(), includedFile))
		}
	}

	//#endregion Visit each project directory and make a list of files to backup

	if *dryRun {
		fmt.Println("Simulating changes to backup directory:")
		fmt.Println()
	}

	//#region Make the necessary changes to the backup directory

	for _, projectFileRelPath := range projectFiles {
		projectFilePath := filepath.Join(*projectsPath, projectFileRelPath)

		// Deleted files can appear in the git change list. Will be removed later.
		if _, err := os.Stat(projectFilePath); os.IsNotExist(err) {
			continue
		}

		if _, ok := backedUpFileRelPaths[projectFileRelPath]; ok {
			delete(backedUpFileRelPaths, projectFileRelPath)

			diffStdout, _ := exec.Command(
				"git", "--no-pager", "diff", "--no-index", "--name-only",
				projectFilePath,
				filepath.Join(*backupPath, projectFileRelPath),
			).Output()

			// No diff output means the file hasn't changed
			if len(diffStdout) == 0 {
				continue
			}
		}

		// Copy files that are changed or newly added
		if *dryRun {
			fmt.Println("+", projectFileRelPath)
		} else {
			err := copyFile(projectFilePath, filepath.Join(*backupPath, projectFileRelPath))
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	// Removing files from backup folder that are no longer in the project
	for backupFileRelPath := range backedUpFileRelPaths {
		if *dryRun {
			fmt.Println("-", backupFileRelPath)
		} else {
			err := os.Remove(filepath.Join(*backupPath, backupFileRelPath))
			if err != nil {
				fmt.Println(err)
			}
		}
	}

	//#region Cleanup empty dirs
	if !*dryRun {
		// Skipping the 0th item because it's the root path of the backup dir itself.
		for i := len(backedUpDirRelPaths) - 1; i > 0; i-- {
			dirFullPath := filepath.Join(*backupPath, backedUpDirRelPaths[i])

			err := os.Remove(dirFullPath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					// The dir path doesn't exist; it may have been deleted during other steps.
					continue
				}

				var pathErr *os.PathError
				if errors.As(err, &pathErr) {
					if sysErr, ok := pathErr.Err.(syscall.Errno); ok {
						switch sysErr {
						case ERROR_ACCESS_DENIED:
							err := os.Chmod(dirFullPath, 0755)
							if err == nil {
								err := os.Remove(dirFullPath)
								if err != nil {
									fmt.Println(err)
								}
							}
						case ERROR_DIR_NOT_EMPTY:
							continue
						default:
							fmt.Println(err)
						}
					}
				} else {
					fmt.Println(err)
				}
			}
		}
	}
	//#endregion Cleanup empty dirs

	//#endregion Make the necessary changes to the backup directory
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	if path == "~" {
		return homeDir, nil
	}

	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(homeDir, path[2:]), nil
	}

	return path, nil
}

func validateConfiguredPaths(projectsPath, backupPath string) error {
	projectsAbsPath, err := filepath.Abs(projectsPath)
	if err != nil {
		return err
	}

	backupAbsPath, err := filepath.Abs(backupPath)
	if err != nil {
		return err
	}

	if samePath(projectsAbsPath, backupAbsPath) {
		return fmt.Errorf("backup directory must be different from projects directory")
	}

	if pathWithin(backupAbsPath, projectsAbsPath) {
		return fmt.Errorf("backup directory must not be inside projects directory")
	}

	if pathWithin(projectsAbsPath, backupAbsPath) {
		return fmt.Errorf("projects directory must not be inside backup directory")
	}

	return nil
}

func samePath(firstPath, secondPath string) bool {
	firstPath = filepath.Clean(firstPath)
	secondPath = filepath.Clean(secondPath)

	if runtime.GOOS == "windows" {
		return strings.EqualFold(firstPath, secondPath)
	}

	return firstPath == secondPath
}

func pathWithin(childPath, parentPath string) bool {
	relPath, err := filepath.Rel(filepath.Clean(parentPath), filepath.Clean(childPath))
	if err != nil {
		return false
	}

	return relPath != "." &&
		relPath != ".." &&
		!strings.HasPrefix(relPath, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relPath)
}

func normalizeForceInclude(path string) (string, error) {
	normalizedPath := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))

	if normalizedPath == "." || normalizedPath == "" {
		return "", fmt.Errorf("force-include path cannot be empty")
	}

	if filepath.IsAbs(normalizedPath) {
		return "", fmt.Errorf("force-include path must be relative: %s", path)
	}

	if normalizedPath == ".." || strings.HasPrefix(normalizedPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("force-include path must stay inside each project: %s", path)
	}

	return normalizedPath, nil
}

func gitFilesToBackup(projectDirPath, remoteName string) ([]string, error) {
	includedFiles := make(StringSet)

	gitCommands := [][]string{
		{"ls-files", "--exclude-standard", "--others", "--full-name"},
		{"diff", "--name-only"},
		{"diff", "--name-only", "--cached"},
	}

	for _, args := range gitCommands {
		stdout, err := runGit(projectDirPath, args...)
		if err != nil {
			return nil, err
		}

		addGitOutputFiles(includedFiles, stdout)
	}

	upstreamRef, err := findUpstreamRef(projectDirPath, remoteName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", err)
	} else if upstreamRef != "" {
		mergeBaseStdout, err := runGit(projectDirPath, "merge-base", "HEAD", upstreamRef)
		if err != nil {
			return nil, err
		}

		mergeBase := strings.TrimSpace(string(mergeBaseStdout))
		unpushedFilesStdout, err := runGit(projectDirPath, "diff", "--name-only", mergeBase, "HEAD")
		if err != nil {
			return nil, err
		}

		addGitOutputFiles(includedFiles, unpushedFilesStdout)
	}

	return sortedSetValues(includedFiles), nil
}

func findUpstreamRef(projectDirPath, remoteName string) (string, error) {
	upstreamStdout, err := runGit(projectDirPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err == nil {
		return strings.TrimSpace(string(upstreamStdout)), nil
	}

	branchNameStdout, err := runGit(projectDirPath, "branch", "--show-current")
	if err != nil {
		return "", err
	}

	branchName := strings.TrimSpace(string(branchNameStdout))
	if branchName == "" || remoteName == "" {
		return "", nil
	}

	remoteRef := remoteName + "/" + branchName
	_, err = runGit(projectDirPath, "rev-parse", "--verify", "--quiet", remoteRef)
	if err != nil {
		return "", fmt.Errorf("no upstream configured and remote ref %q was not found", remoteRef)
	}

	return remoteRef, nil
}

func runGit(dir string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"--no-pager"}, args...)
	cmd := exec.Command("git", commandArgs...)
	cmd.Dir = dir

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout, nil
}

func addGitOutputFiles(files StringSet, stdout []byte) {
	for _, line := range strings.Split(string(stdout), "\n") {
		path := filepath.FromSlash(strings.TrimSpace(line))
		if path == "" {
			continue
		}

		files[path] = struct{}{}
	}
}

func sortedSetValues(values StringSet) []string {
	sortedValues := make([]string, 0, len(values))
	for value := range values {
		sortedValues = append(sortedValues, value)
	}

	sort.Strings(sortedValues)

	return sortedValues
}

func copyFile(srcPath, dstPath string) error {
	// Create the destination directory if it doesn't exist
	dstDir := filepath.Dir(dstPath)
	_, err := os.Stat(dstDir)
	if err != nil && os.IsNotExist(err) {
		err := os.MkdirAll(dstDir, 0755)
		if err != nil {
			return err
		}
	}

	// Open the source file for reading
	sourceFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create the destination file if it doesn't exist
	destinationFile, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	// Copy the contents of the source file to the destination file
	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}

	// Preserve the file permissions of the source file
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(dstPath, srcInfo.Mode()); err != nil {
		return err
	}

	return nil
}

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}
