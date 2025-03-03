package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	*fileNames = append(*fileNames, filepath.FromSlash(value))

	return nil
}

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
  … basically every unpushed file that can be lost during an incident.

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

	if strings.HasPrefix(*projectsPath, "~") {
		homeDir, err := os.UserHomeDir()
		panicIf(err)
		*projectsPath = filepath.Join(homeDir, (*projectsPath)[1:])
	}

	if strings.HasPrefix(*backupPath, "~") {
		homeDir, err := os.UserHomeDir()
		panicIf(err)
		*backupPath = filepath.Join(homeDir, (*backupPath)[1:])
	}

	//#endregion Parse flags

	// Check if git is installed
	_, err := exec.LookPath("git")
	panicIf(err)

	//#region Read the full backup directory

	backedUpDirRelPaths := []string{}

	type StringSet map[string]struct{}
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

		// `cd` into the project directory
		err := os.Chdir(projectDirPath)
		panicIf(err)

		// --exclude-standard: Ignore .gitignore and other git excluded files
		// --others: Untracked files not yet added by `git add`
		// --full-name: Output relative paths
		untrackedFilesStdout, err := exec.Command(
			"git", "--no-pager", "ls-files", "--exclude-standard", "--others", "--full-name",
		).Output()
		panicIf(err)

		includedFiles := strings.Split(filepath.FromSlash(string(untrackedFilesStdout)), "\n")

		branchNameStdout, err := exec.Command(
			"git", "--no-pager", "branch", "--show-current",
		).Output()
		panicIf(err)
		branchName := strings.TrimSpace(string(branchNameStdout))

		// Current branch name can be empty when a specific commit is checked out
		if branchName != "" {
			// Files that are in local commits but not yet pushed to the remote
			unpushedFilesStdout, _ := exec.Command(
				"git", "--no-pager", "diff", "--name-only", *remoteBranch+"/"+branchName,
			).Output()
			unpushedFiles := strings.Split(filepath.FromSlash(string(unpushedFilesStdout)), "\n")

			includedFiles = append(includedFiles, unpushedFiles...)
		}

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
							err := os.Chmod(dirFullPath, 0700)
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
