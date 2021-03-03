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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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

const gcsFetchTimeout = 10 * time.Second

type Reconciler struct {
	cachePath         string
	defaultBundle     string
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

func (r *Reconciler) getFile(bucket, path string) ([]byte, error) {
	filename := filepath.Base(path)
	if filename == "." {
		return nil, fmt.Errorf("cannot resolve base from path %q", path)
	}
	fileID := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int31())
	filename = fmt.Sprintf("%s-%s", filename, fileID)
	destination := filepath.Join(r.cachePath, filename)

	if !strings.HasPrefix(bucket, "gs://") {
		bucket = "gs://" + bucket
	}

	if strings.HasSuffix(bucket, "/") {
		bucket = bucket[:len(bucket)-1]
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	gsURL := fmt.Sprintf("%s%s", bucket, path)

	ctx, cancel := context.WithTimeout(context.Background(), gcsFetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gsutil", "cp", gsURL, destination)

	log.Printf("Running [%s %s]", cmd.Path, strings.Join(cmd.Args, " "))
	logs, err := cmd.CombinedOutput()
	defer os.Remove(destination)

	debugLog(string(logs))

	if err != nil {
		debugLog(err.Error())
		return nil, err
	}

	return ioutil.ReadFile(destination)
}

func (r *Reconciler) reconcile(ctx context.Context, run *v1alpha1.Run) reconciler.Event {
	logging.FromContext(ctx).Debugf("RECONCILING RUN")

	if strings.TrimSpace(run.Spec.Ref.Bundle) == "" {
		run.Spec.Ref.Bundle = r.defaultBundle
	}

	if strings.TrimSpace(run.Spec.Ref.Name) == "" {
		markUnresolved(run, "MissingName", "spec.ref.name missing, expected /path/to/resource.yaml")
		return nil
	}

	bucket := strings.TrimSpace(run.Spec.Ref.Bundle)
	path := strings.TrimSpace(run.Spec.Ref.Name)

	taskYAML, err := r.getFile(bucket, path)
	if err != nil {
		markUnresolved(run, "ErrorResolvingTask", err.Error())
		return nil
	}

	task, err := convertResourceYAMLToTask(taskYAML)
	if err != nil {
		markUnresolved(run, "ErrorParsingTaskYAML", err.Error())
		return nil
	}

	taskJSON, err := json.Marshal(task)
	if err != nil {
		markUnresolved(run, "ErrorSerializingTask", err.Error())
		return nil
	}

	run.Status.ExtraFields.Raw = taskJSON
	run.Status.SetCondition(&apis.Condition{
		Type:   ConditionTaskResolved,
		Status: v1.ConditionTrue,
	})

	if _, err = r.pipelineClientSet.TektonV1alpha1().Runs(run.Namespace).Update(ctx, run, metav1.UpdateOptions{}); err != nil {
		logging.FromContext(ctx).Errorf("error updating run %s/%s: %v", run.Namespace, run.Name, err)
		// TODO: We can get here because generation is invalid (parallel racey updates). Should this
		// be a failure or should we simply return and wait for the next reconcile to occur with the new generation?
		// run.Status.MarkRunFailed("RunUpdateError", "%v", err)
		// return nil
	}

	return reconciler.NewEvent(v1.EventTypeNormal, "RunReconciled", "Run reconciled: \"%s/%s\"", run.Namespace, run.Name)
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
