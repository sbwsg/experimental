package resolve

import (
	"io/ioutil"
	"os"
	"strings"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"sigs.k8s.io/yaml"
)

func ResolvePipelineRun(path string) (*v1beta1.PipelineRun, error) {
	pr := new(v1beta1.PipelineRun)
	if err := read(path, pr); err != nil {
		return nil, err
	}

	// Resolve Pipeline (either by file or by API)
	spec := pr.Spec.PipelineSpec
	if pr.Spec.PipelineRef != nil {
		name := pr.Spec.PipelineRef.Name
		p := new(v1beta1.Pipeline)
		if err := resolve(name, p); err != nil {
			return nil, err
		}
		spec = &p.Spec
	}

	// Resolve PipelineTasks
	for i, pt := range spec.Tasks {
		if pt.TaskRef == nil {
			// Not a ref - nothing to do.
			continue
		}

		var ts v1beta1.TaskSpec
		name := pt.TaskRef.Name
		t := new(v1beta1.Task)
		if err := resolve(name, t); err != nil {
			return nil, err
		}
		ts = t.Spec

		pt.TaskSpec = &v1beta1.EmbeddedTask{TaskSpec: ts}
		pt.TaskRef = nil
		spec.Tasks[i] = pt
	}

	pr.Spec.PipelineSpec = spec
	pr.Spec.PipelineRef = nil
	return pr, nil
}

func resolve(name string, out interface{}) error {
	switch {
	case isFile(name):
		return read(name, out)
	case isGit(name):
		return readGit(name, out)
	default:
		// Resolve from k8s API?
		return nil
	}
}

func isFile(name string) bool {
	return strings.HasPrefix(name, "./") || strings.HasPrefix(name, "/")
}

func read(path string, out interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		return err
	}
	return nil
}

func ResolveTaskRun(path string) (*v1beta1.TaskRun, error) {
	tr := new(v1beta1.TaskRun)
	if err := read(path, tr); err != nil {
		return nil, err
	}

	if tr.Spec.TaskRef == nil {
		return tr, nil
	}

	var ts v1beta1.TaskSpec
	name := tr.Spec.TaskRef.Name
	t := new(v1beta1.Task)
	if err := resolve(name, t); err != nil {
		return nil, err
	}
	ts = t.Spec

	tr.Status.TaskSpec = &ts
	tr.Spec.TaskRef = nil

	return tr, nil
}
