package catalog

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/deprecated/scheme"
)

// "git-clone" = "git-clone/0.3/git-clone" (always use latest version)
// "git-clone--0.2" = "git-clone/0.2/git-clone"
// Get returns the bytes of the task yaml from the catalog repo.
func (c *Catalog) Get(name string) ([]byte, error) {
	version := ""

	if strings.Contains(name, "--") {
		s := strings.Split(name, "--")
		name = s[0]
		version = s[1]
	} else {
		root := filepath.Join(c.dir, fmt.Sprintf("/%s/%s", c.kind, name))
		ver, err := c.findLatestVersion(root)
		if err != nil {
			return nil, fmt.Errorf("error scanning %q: %v", root, err)
		}
		version = ver
	}

	path := fmt.Sprintf("/%s/%s/%s/%s.yaml", c.kind, name, version, name)

	yaml, err := c.readResourceFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading file %q: %v", path, err)
	}

	return yaml, nil
}

func (c *Catalog) readResourceFromFile(path string) ([]byte, error) {
	yamlPath := filepath.Join(c.dir, path)
	yamlBytes, err := ioutil.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("invalid yaml in %q: %v", path, err)
	}
	return yamlBytes, nil
}

func (c *Catalog) cloneRepo() error {
	cmd := exec.Command("git", "clone", c.repoURL, c.dir)
	gitLogs, err := cmd.CombinedOutput()
	log.Println(string(gitLogs))
	if err != nil {
		return fmt.Errorf("error cloning repo %q: %v", c.repoURL, err)
	}
	return nil
}

// findLatestVersion finds the latest catalog entry version from a entry's root directory
// Given a root directory structed as root/0.3, root/0.2, root/0.1 this function should return "0.3"
func (c *Catalog) findLatestVersion(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	names := []string{}
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".") {
			continue
		}
		if ent.IsDir() {
			names = append(names, ent.Name())
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no versions found in root %v", root)
	}

	// TODO(sbwsg): sort by semver instead of just alphabetical
	sort.Strings(names)

	return names[len(names)-1], nil
}

func (c *Catalog) ConvertResourceYAMLToTask(yaml []byte) (runtime.Object, error) {
	decoder := scheme.Codecs.UniversalDeserializer()
	t := v1beta1.Task{}
	obj, _, err := decoder.Decode(yaml, nil, &t)
	if err != nil {
		return nil, err
	}

	return obj, nil
}
