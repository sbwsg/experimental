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

package gcsref

import (
	context "context"
	"log"
	"os"

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
	ControllerName = "gcsref-controller"
	apiVersion     = "gcsref.tekton.dev/v1alpha1"
	kind           = "Task"
)

var cachePath = os.Getenv("CACHE_PATH")
var defaultBundle = os.Getenv("DEFAULT_BUNDLE")

func NewController(ctx context.Context, cmw configmap.Watcher) *controller.Impl {
	logger := logging.FromContext(ctx)

	runInformer := run.Get(ctx)

	if defaultBundle == "" {
		defaultBundle = "https://github.com/tektoncd/catalog.git"
	}

	reconciler := &Reconciler{
		cachePath:         cachePath,
		defaultBundle:     defaultBundle,
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
			// Not a gcsref.tekton.dev/v1alpha1.Task!
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
