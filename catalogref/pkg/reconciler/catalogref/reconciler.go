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
	"encoding/json"
	"fmt"
	"strings"

	catalog "github.com/tektoncd/experimental/catalogref/pkg/catalog"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/run"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/deprecated/scheme"
	"knative.dev/pkg/apis"
	logging "knative.dev/pkg/logging"
	reconciler "knative.dev/pkg/reconciler"
)

// Ensure reconciler implements Interface
var _ run.Interface = (*Reconciler)(nil)

type Reconciler struct {
	defaultCatalog    string
	catalogs          map[string]catalog.Catalog
	pipelineClientSet clientset.Interface
}

var ConditionTaskResolved = apis.ConditionType("TaskResolved")

func debugLog(format string, args ...interface{}) {
	wrappedFormat := fmt.Sprintf("\n\n\n%s\n\n", format)
	fmt.Printf(wrappedFormat, args...)
}

func (r *Reconciler) ReconcileKind(ctx context.Context, run *v1alpha1.Run) reconciler.Event {
	logger := logging.FromContext(ctx)

	namespace := run.ObjectMeta.Namespace
	name := run.ObjectMeta.Name

	logger.Infof("Reconciling Run %s/%s", namespace, name)

	if !run.HasStarted() {
		logging.FromContext(ctx).Debugf("RUN HAS NOT STARTED")
		run.Status.InitializeConditions()
		r.syncTime(ctx, run)
	}

	return r.reconcile(ctx, run)
}

// syncTime checks in case node time was not synchronized
// when controller has been scheduled to other nodes.
func (r *Reconciler) syncTime(ctx context.Context, run *v1alpha1.Run) {
	logger := logging.FromContext(ctx)
	if run.Status.StartTime.Sub(run.CreationTimestamp.Time) < 0 {
		logger.Warnf("Run %s/%s createTimestamp %s is after the Run started %s", run.Namespace, run.Name, run.CreationTimestamp, run.Status.StartTime)
		run.Status.StartTime = &run.CreationTimestamp
	}
}

func (r *Reconciler) reconcile(ctx context.Context, run *v1alpha1.Run) reconciler.Event {
	logging.FromContext(ctx).Debugf("RECONCILING RUN")

	namespace := run.ObjectMeta.Namespace
	name := run.ObjectMeta.Name

	if run.Spec.Ref.Bundle == "" {
		run.Spec.Ref.Bundle = r.defaultCatalog
	}

	catalogName := run.Spec.Ref.Bundle

	cat, ok := r.catalogs[catalogName]
	if !ok {
		supportedCatalogs := []string{}
		for name := range r.catalogs {
			supportedCatalogs = append(supportedCatalogs, fmt.Sprintf("%q", name))
		}
		cats := strings.Join(supportedCatalogs, ", ")
		markUnresolved(run, "InvalidBundle", fmt.Sprintf("no catalog named %q configured, only %s catalogs currently supported", catalogName, cats))
		return nil
	}

	if run.Spec.Ref.Name == "" {
		markUnresolved(run, "MissingCatalogRefName", "spec.ref.name missing")
		return nil
	}

	taskName := run.Spec.Ref.Name
	taskYAML, err := cat.Get(taskName)
	if err != nil {
		markUnresolved(run, "ErrorResolvingCatalogRef", err.Error())
		return nil
	}

	task, err := convertResourceYAMLToTask(taskYAML)
	if err != nil {
		markUnresolved(run, "ErrorParsingCatalogRefAsYAML", err.Error())
		return nil
	}

	taskJSON, err := json.Marshal(task)
	if err != nil {
		markUnresolved(run, "ErrorSerializingCatalogRefAsJSON", err.Error())
		return nil
	}

	run.Status.ExtraFields.Raw = taskJSON
	run.Status.SetCondition(&apis.Condition{
		Type:   ConditionTaskResolved,
		Status: v1.ConditionTrue,
	})

	if _, err = r.pipelineClientSet.TektonV1alpha1().Runs(run.Namespace).Update(ctx, run, metav1.UpdateOptions{}); err != nil {
		logging.FromContext(ctx).Errorf("error updating run %s/%s: %v", namespace, name, err)
		// TODO: We can get here because generation is invalid (parallel racey updates). Should this
		// be a failure or should we simply return and wait for the next reconcile to occur with the new generation?
		// run.Status.MarkRunFailed("RunUpdateError", "%v", err)
		// return nil
	}

	return reconciler.NewEvent(v1.EventTypeNormal, "RunReconciled", "Run reconciled: \"%s/%s\"", namespace, name)
}

func getParam(run *v1alpha1.Run, name string) string {
	if run != nil {
		for _, p := range run.Spec.Params {
			if p.Name == name {
				return p.Value.StringVal
			}
		}
	}
	return ""
}

func markUnresolved(run *v1alpha1.Run, reason, message string) {
	run.Status.SetCondition(&apis.Condition{
		Type:    ConditionTaskResolved,
		Status:  v1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
	run.Status.MarkRunFailed(reason, message)
}

func convertResourceYAMLToTask(yaml []byte) (runtime.Object, error) {
	decoder := scheme.Codecs.UniversalDeserializer()
	t := v1beta1.Task{}
	obj, _, err := decoder.Decode(yaml, nil, &t)
	if err != nil {
		return nil, err
	}

	return obj, nil
}
