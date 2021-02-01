package resolve

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func isGit(name string) bool {
	return strings.HasPrefix(name, "git://")
}

func readGit(name string, out interface{}) error {
	url, ref, filename, err := parseGitName(name)
	dir, err := ioutil.TempDir("", "tkn-resolve-git")
	if err != nil {
		return fmt.Errorf("error reading from git: %w", err)
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("git", "clone", url, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error cloning %q: %w", name, err)
	}
	if ref != "" {
		cmd := exec.Command("git", "checkout", ref)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("error checking out ref %q of %q: %w", ref, name, err)
		}
	}
	read(filepath.Join(dir, filename), out)
	return nil
}

func parseGitName(name string) (string, string, string, error) {
	// git://git@github.com:tektoncd/pipeline.git;/path/to/file
	// git://https://github.com/tektoncd/pipeline.git;/path/to/file
	// git://github.com/tektoncd/pipeline.git;/path/to/file
	// git://github.com/tektoncd/pipeline.git;my-branch;/path/to/file
	// git://github.com/tektoncd/pipeline.git;f4de32;/path/to/file
	name = strings.TrimPrefix(name, "git://")
	nameParts := strings.Split(name, ";")
	switch len(nameParts) {
	case 2:
		return nameParts[0], "", nameParts[1], nil
	case 3:
		return nameParts[0], nameParts[1], nameParts[2], nil
	case 1:
		fallthrough
	default:
		return "", "", "", fmt.Errorf("invalid git reference %q", name)
	}
}
