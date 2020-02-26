/*
Copyright 2020 Brad Beam.

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

package controllers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lightwatchv1alpha1 "github.com/bradbeam/lightstream/api/v1alpha1"
)

// EnvWatcherReconciler reconciles a EnvWatcher object
type EnvWatcherReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	// cache tracks the resource's goroutine with a context's cancelFunc
	// so we can cancel/stop them upon update/deletion
	sync.Mutex
	cache map[types.NamespacedName]context.CancelFunc
}

// +kubebuilder:rbac:groups=lightwatch.vigilant.dev,resources=envwatchers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lightwatch.vigilant.dev,resources=envwatchers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get;update;patch

// Reconcile is the main control loop. A request specifying the watched resource name and namespace
// triggers the decision process.
func (r *EnvWatcherReconciler) Reconcile(req ctrl.Request) (res ctrl.Result, err error) {
	r.Log.Info(fmt.Sprintf("reconcile loop starting for %v", req.NamespacedName))
	defer r.Log.Info(fmt.Sprintf("reconcile loop finished for %v", req.NamespacedName))

	ctx := context.Background()

	_ = r.Log.WithValues("envwatcher", req.NamespacedName)

	if r.cache == nil {
		r.cache = make(map[types.NamespacedName]context.CancelFunc)
	}

	eWatch := &lightwatchv1alpha1.EnvWatcher{}

	// Identify the resource
	if err = r.Get(ctx, req.NamespacedName, eWatch); err != nil {
		// If we cant find the resource, then assume it's been deleted
		if errors.IsNotFound(err) {
			return res, r.cleanupWatcher(ctx, req.NamespacedName)
		}
		return res, err
	}

	// Handle case where CRD being deleted.
	// This is probably overkill with the logic above to handle cleanup if the
	// resource is no longer found, but this way we can try to do the right thing
	// first and rely on the above as a catchall.
	if !eWatch.ObjectMeta.DeletionTimestamp.IsZero() {
		return res, r.cleanupWatcher(ctx, req.NamespacedName)
	}

	// Compare the last generation (version)  of the resource we've handled to the current
	// resource version. Allow us to skip noop situations
	if eWatch.ObjectMeta.Generation == eWatch.Status.ObservedGeneration {
		return res, err
	}

	// If we get an update, interrupt running goroutine
	if _, ok := r.cache[req.NamespacedName]; ok {
		r.Log.Info(fmt.Sprintf("received an update for %v, canceling currently running watcher", req.NamespacedName))
		r.Lock()
		r.cache[req.NamespacedName]()
		r.Unlock()
	}

	// Kick off goroutine for each env watch CRD
	go func() {
		r.Log.Info(fmt.Sprintf("scheduling new watcher for %v", req.NamespacedName))
		// Not sure if we want to try to handle requeueing logic here since we return
		// below
		r.watcher(ctx, req.NamespacedName, eWatch)
	}()

	return res, nil
}

// cleanupWatcher handles terminating any currently running goroutine associated
// with the env watcher resource as well as deleting the configmap associated
// with the env watcher.
func (r *EnvWatcherReconciler) cleanupWatcher(ctx context.Context, namespacedName types.NamespacedName) (err error) {
	r.Lock()
	defer r.Unlock()

	if cancelfn, ok := r.cache[namespacedName]; ok {
		cancelfn()
	}

	cfgMap := &corev1.ConfigMap{}
	if err = r.Get(ctx, namespacedName, cfgMap); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.Delete(ctx, cfgMap)
}

// SetupWithManager starts the main control loop for the controller.
func (r *EnvWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lightwatchv1alpha1.EnvWatcher{}).
		Complete(r)
}

// watcher handles the recurring task of fetching a remote resource.
func (r *EnvWatcherReconciler) watcher(pctx context.Context, namespacedName types.NamespacedName, eWatch *lightwatchv1alpha1.EnvWatcher) {
	ctx, cancel := context.WithCancel(pctx)

	// Ensure we keep track of the goroutine contexts associated with each env watcher
	r.Lock()

	// Explicitly call the cancelFunc again so we dont risk any runaway goroutines.
	if _, ok := r.cache[namespacedName]; ok {
		r.cache[namespacedName]()
	}

	r.cache[namespacedName] = cancel
	r.Unlock()

	period, err := time.ParseDuration(eWatch.Spec.Frequency)
	if err != nil {
		r.Log.Error(err, "failed to parse frequency", "frequency", eWatch.Spec.Frequency)
		return
	}

	// This means now() is after last check + period
	if time.Now().After(time.Unix(eWatch.Status.LastCheck, 0).Add(period)) {
		r.doStuff(ctx, eWatch)
	} else {
		// Wait difference between now and next iteration
		<-time.After(time.Now().Sub(time.Unix(eWatch.Status.LastCheck, 0).Add(period)))
		r.doStuff(ctx, eWatch)
	}

	// instantiate a new ticker
	watcherTicker := time.NewTicker(period)
	defer watcherTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-watcherTicker.C:
			r.doStuff(ctx, eWatch)
		}
	}
}

// doStuff handles the fetching of a remote resource and deciding what to do.
// First, the remote file is fetched and checksum calculated. If the checksum
// matches the existing file, the EnvWatcher resource's lastCheck will be updated
// and no other action is performed.
// If the destination configmap does not exist, it will be (re-)created.
// Any line in the remote file that does not have exactly 2 fields separated by
// an '=' will be discarded.
func (r *EnvWatcherReconciler) doStuff(ctx context.Context, eWatch *lightwatchv1alpha1.EnvWatcher) {
	var (
		data           []byte
		err            error
		namespacedName = types.NamespacedName{Name: eWatch.ObjectMeta.Name, Namespace: eWatch.ObjectMeta.Namespace}
	)

	if data, err = downloadFile(eWatch.Spec.URL); err != nil {
		r.Log.Error(err, "failed to download file", "url", eWatch.Spec.URL)
		return
	}

	remoteChecksum := checksumData(data)

	eWatch.Status.LastCheck = time.Now().Unix()

	// discover configmap
	var needToRecreateCfgMap bool
	cfgMap := &corev1.ConfigMap{}
	if err = r.Get(ctx, namespacedName, cfgMap); err != nil {
		if errors.IsNotFound(err) {
			// create base configmap
			cfgMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespacedName.Namespace,
					Name:      namespacedName.Name,
				},
				Data: make(map[string]string),
			}

			if err = r.Create(ctx, cfgMap); err != nil {
				r.Log.Error(err, "failed to create configmap", "configmap", namespacedName)
				return
			}
			needToRecreateCfgMap = true
		} else {
			r.Log.Error(err, "failed to find configmap", "configmap", namespacedName)
			return
		}
	} else {
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "=")
		if len(fields) != 2 {
			r.Log.Info(fmt.Sprintf("ignoring invalid line for %v: %s", namespacedName, scanner.Text()))
			continue
		}
		cfgMap.Data[fields[0]] = fields[1]
	}
	if err := scanner.Err(); err != nil {
		r.Log.Error(err, "failed to parse downloaded file", "contents", string(data))
		return
	}

	if (eWatch.Status.Checksum != remoteChecksum) || needToRecreateCfgMap {
		eWatch.Status.Checksum = remoteChecksum

		if err = r.Update(ctx, cfgMap); err != nil {
			r.Log.Error(err, "failed to update configmap")
		}
	}

	// So we can keep track of the last version we saw
	eWatch.Status.ObservedGeneration = eWatch.ObjectMeta.Generation

	if err = r.Status().Update(ctx, eWatch); err != nil {
		r.Log.Error(err, "failed to update env watcher status")
	}
}

// downloadFile downloads a remote file and returns the contents of the file.
func downloadFile(downloadFile string) (data []byte, err error) {
	if _, err = url.Parse(downloadFile); err != nil {
		return data, err
	}

	var resp *http.Response
	resp, err = http.Get(downloadFile)
	if err != nil {
		return data, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return data, fmt.Errorf("failed to download %s with status code %d", downloadFile, resp.StatusCode)
	}

	return ioutil.ReadAll(resp.Body)
}

// checksumData calculates the sha256 hash of a given []byte.
func checksumData(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
