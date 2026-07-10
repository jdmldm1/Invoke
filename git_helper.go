package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitFileStatus struct {
	Path   string
	Status string // "added", "modified", "deleted", "untracked", "staged"
}

type GitRepoStatus struct {
	IsRepo bool
	Branch string
	Files  []GitFileStatus
}

func getGitStatus(dir string) GitRepoStatus {
	// Check inside work tree
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = dir
	err := cmd.Run()
	if err != nil {
		return GitRepoStatus{IsRepo: false}
	}

	// Branch
	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = dir
	branchOut, _ := branchCmd.Output()
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		branchCmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		branchCmd.Dir = dir
		branchOut, _ = branchCmd.Output()
		branch = strings.TrimSpace(string(branchOut))
	}

	// Status porcelain
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()

	var files []GitFileStatus
	lines := strings.Split(string(statusOut), "\n")
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		statusCode := line[:2]
		filePath := strings.TrimSpace(line[2:])

		status := "modified"
		if strings.Contains(statusCode, "?") {
			status = "untracked"
		} else if strings.Contains(statusCode, "A") {
			status = "added"
		} else if strings.Contains(statusCode, "D") {
			status = "deleted"
		} else if statusCode[0] != ' ' && statusCode[1] == ' ' {
			status = "staged"
		}

		files = append(files, GitFileStatus{
			Path:   filePath,
			Status: status,
		})
	}

	return GitRepoStatus{
		IsRepo: true,
		Branch: branch,
		Files:  files,
	}
}

func gitStageFile(dir, file string) error {
	cmd := exec.Command("git", "add", file)
	cmd.Dir = dir
	return cmd.Run()
}

func gitUnstageFile(dir, file string) error {
	// Use restore --staged
	cmd := exec.Command("git", "restore", "--staged", file)
	cmd.Dir = dir
	return cmd.Run()
}

func gitGetDiff(dir, file string) string {
	var cmd *exec.Cmd
	// If file status is staged, run git diff --cached
	status := getGitStatus(dir)
	isStaged := false
	for _, f := range status.Files {
		if f.Path == file && f.Status == "staged" {
			isStaged = true
			break
		}
	}

	if isStaged {
		cmd = exec.Command("git", "diff", "--cached", file)
	} else {
		cmd = exec.Command("git", "diff", file)
	}
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	_ = cmd.Run()
	return outBytes.String()
}

func gitCommit(dir, msg string) (string, error) {
	cmd := exec.Command("git", "commit", "-m", msg)
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitGetBranches(dir string) ([]string, error) {
	cmd := exec.Command("git", "branch")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var branches []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "* ") {
			line = strings.TrimPrefix(line, "* ")
		}
		branches = append(branches, line)
	}
	return branches, nil
}

func gitCheckout(dir, branch string) (string, error) {
	cmd := exec.Command("git", "checkout", branch)
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitPull(dir string) (string, error) {
	cmd := exec.Command("git", "pull")
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitPush(dir string) (string, error) {
	cmd := exec.Command("git", "push")
	cmd.Dir = dir
	var outBytes bytes.Buffer
	cmd.Stdout = &outBytes
	cmd.Stderr = &outBytes
	err := cmd.Run()
	return outBytes.String(), err
}

func gitStageAll(dir string) error {
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	return cmd.Run()
}

// gitDiscardFile permanently reverts working-tree changes for a single file.
// Untracked files are deleted; tracked files are restored to HEAD.
func gitDiscardFile(dir string, f GitFileStatus) (string, error) {
	if f.Status == "untracked" {
		if err := os.Remove(filepath.Join(dir, f.Path)); err != nil {
			return "", err
		}
		return "Deleted untracked file: " + f.Path, nil
	}

	// Unstage first (no-op if it wasn't staged), then restore the working tree.
	unstage := exec.Command("git", "restore", "--staged", f.Path)
	unstage.Dir = dir
	_ = unstage.Run()

	cmd := exec.Command("git", "restore", f.Path)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		// Fall back to checkout for older git versions.
		cmd2 := exec.Command("git", "checkout", "--", f.Path)
		cmd2.Dir = dir
		var out2 bytes.Buffer
		cmd2.Stdout = &out2
		cmd2.Stderr = &out2
		if err2 := cmd2.Run(); err2 != nil {
			return out.String() + out2.String(), err2
		}
	}
	return "Discarded changes in: " + f.Path, nil
}

// gitFileAtHead returns the committed (HEAD) contents of a file. Returns an
// empty string for new/untracked files or when not in a repo.
func gitFileAtHead(absPath string) string {
	dir := filepath.Dir(absPath)
	rootCmd := exec.Command("git", "rev-parse", "--show-toplevel")
	rootCmd.Dir = dir
	rootOut, err := rootCmd.Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(rootOut))
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return ""
	}
	showCmd := exec.Command("git", "show", "HEAD:"+filepath.ToSlash(rel))
	showCmd.Dir = dir
	out, err := showCmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func gitGetStagedDiff(dir string) string {
	cmd := exec.Command("git", "diff", "--staged")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

// gitGetWorkingDiff returns all uncommitted changes vs HEAD (staged + unstaged).
func gitGetWorkingDiff(dir string) string {
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func gitGetLog(dir string, n int) string {
	cmd := exec.Command("git", "log", fmt.Sprintf("-%d", n), "--pretty=format:%h  %ad  %s", "--date=short")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "Could not read git log: " + err.Error()
	}
	if strings.TrimSpace(string(out)) == "" {
		return "No commits yet."
	}
	return string(out)
}
