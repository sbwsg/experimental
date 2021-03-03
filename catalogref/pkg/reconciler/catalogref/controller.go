/*
Copyright 2021 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package catalogref

import (
	context "context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	catalog "github.com/tektoncd/experimental/catalogref/pkg/catalog"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	pipelineclient "github.com/tektoncd/pipeline/pkg/client/injection/client"
	run "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/run"
	v1alpha1run "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/run"
	"k8s.io/client-go/tools/cache"
	configmap "knative.dev/pkg/configmap"
	controller "knative.dev/pkg/controller"
	logging "knative.dev/pkg/logging"
)

const (
	ControllerName = "catalogref-controller"
	apiVersion     = "catalogref.tekton.dev/v1alpha1"
	kind           = "Task"
)

var cachePath = os.Getenv("CACHE_PATH")
var catalogMapping = os.Getenv("CATALOG_MAPPING")

func NewController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	runInformer := run.Get(ctx)

	if catalogMapping == "" {
		catalogMapping = "tekton=https://github.com/tektoncd/catalog.git"
	}

	catalogs := map[string]catalog.Catalog{}

	defaultCatalog := ""
	mappings := strings.Fields(catalogMapping)
	for _, m := range mappings {
		ent := strings.SplitN(m, "=", 2)
		if len(ent) < 2 {
			logger.Errorf("invalid catalog mapping entry %q", m)
		}
		name := ent[0]
		url := ent[1]

		if defaultCatalog == "" {
			defaultCatalog = name
		}

		scratchDir, err := ioutil.TempDir(cachePath, name+"-catalog")
		if err != nil {
			logger.Errorf("Unable to create temp scratch dir: %v", err)
			return nil
		}

		cat, err := catalog.New(url, "task", scratchDir)
		if err != nil {
			panic(fmt.Sprintf("error setting up catalog %q: %v", name, err))
		}
		catalogs[name] = cat
	}

	for name, cat := range catalogs {
		log.Printf("catalog=%q dir=%q repo=%q", name, cat.GetDir(), cat.GetRepo())
	}

	log.Printf("catalog %q is the default bundle", defaultCatalog)

	reconciler := &Reconciler{
		defaultCatalog:    defaultCatalog,
		catalogs:          catalogs,
		pipelineClientSet: pipelineclient.Get(ctx),
	}

	impl := v1alpha1run.NewImpl(ctx, reconciler, func(impl *controller.Impl) controller.Options {
		return controller.Options{
			AgentName: ControllerName,
		}
	})

	logger.Info("Setting up event handlers")

	runInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
		FilterFunc: FilterResolvedRuns(apiVersion, kind),
		Handler:    controller.HandleAll(impl.Enqueue),
	})

	return impl
}

func FilterResolvedRuns(apiVersion, kind string) func(interface{}) bool {
	return func(obj interface{}) bool {
		r, ok := obj.(*v1alpha1.Run)
		if !ok {
			// Somehow got informed of a non-Run object.
			// Ignore.
			return false
		}
		if r == nil || r.Spec.Ref == nil {
			// These are invalid, but just in case they get
			// created somehow, don't panic.
			return false
		}
		log.Printf("CHECKING IF API VERSION IS KOSHER: %s %s", r.Spec.Ref.APIVersion, r.Spec.Ref.Kind)
		if r.Spec.Ref.APIVersion != apiVersion || r.Spec.Ref.Kind != v1alpha1.TaskKind(kind) {
			// Not a catalogref.tekton.dev/v1alpha1.Task!
			return false
		}
		log.Printf("CHECKING IF CONDITION IS NIL: %v", r.Status.GetCondition("TaskResolved"))
		if cond := r.Status.GetCondition("TaskResolved"); cond != nil {
			// Task resolution has been performed by some other process already.
			return false
		}
		return true
	}
}
