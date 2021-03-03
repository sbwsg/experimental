package git

import (
	"fmt"
	"io/ioutil"
	"log"
	"os/exec"
	"path/filepath"
)

type Server struct {
	// Dir is where the repo is checked out
	Dir string
	// URL is the url to the repo
	URL string
}

// Get returns the bytes of the task yaml from the git repo.
func (s *Server) Get(name string) ([]byte, error) {
	s.cloneRepo()
	yaml, err := s.readResourceFromFile(name)
	if err != nil {
		return nil, fmt.Errorf("error reading file %q: %v", name, err)
	}

	return yaml, nil
}

func (s *Server) readResourceFromFile(path string) ([]byte, error) {
	yamlPath := filepath.Join(s.Dir, path)
	yamlBytes, err := ioutil.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("invalid yaml in %q: %v", path, err)
	}
	return yamlBytes, nil
}

func (s *Server) cloneRepo() error {
	cmd := exec.Command("git", "clone", s.URL, s.Dir)
	gitLogs, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error cloning repo %q: %v", s.URL, err)
	}
	log.Println(string(gitLogs))
	return nil
}
