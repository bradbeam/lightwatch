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

	// We'll use this map to keep track of goroutines to contexts
	// so we can cancel/stop them upon deletion
	sync.Mutex
	cache map[types.NamespacedName]context.CancelFunc
}

// +kubebuilder:rbac:groups=lightwatch.vigilant.dev,resources=envwatchers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lightwatch.vigilant.dev,resources=envwatchers/status,verbs=get;update;patch

func (r *EnvWatcherReconciler) Reconcile(req ctrl.Request) (res ctrl.Result, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	_ = r.Log.WithValues("envwatcher", req.NamespacedName)

	if r.cache == nil {
		r.Lock()
		r.cache = make(map[types.NamespacedName]context.CancelFunc)
		r.Unlock()
	}

	eWatch := &lightwatchv1alpha1.EnvWatcher{}

	r.Log.Info(fmt.Sprintf("Starting reconcile loop for %v", req.NamespacedName))
	defer r.Log.Info(fmt.Sprintf("Finish reconcile loop for %v", req.NamespacedName))

	// Identify the resource
	if err = r.Get(ctx, req.NamespacedName, eWatch); err != nil {
		// If we cant find the resource, then assume it's been deleted
		if errors.IsNotFound(err) {
			// These seem unnecessary because we call cancel in cleanup,
			// but compiler yells at us otherwise
			cancel()
			return res, r.cleanupWatcher(ctx, req.NamespacedName)
		}
		cancel()
		return res, err
	}

	// CRD being deleted.
	// This is probably overkill with the logic above to handle cleanup if the
	// resource is no longer found, but this way we can try to do the right thing first and rely on
	// the above as a catchall.
	if !eWatch.ObjectMeta.DeletionTimestamp.IsZero() {
		cancel()
		return res, r.cleanupWatcher(ctx, req.NamespacedName)
	}

	// If we get an update, interrupt running goroutine
	if _, ok := r.cache[req.NamespacedName]; ok {
		r.Lock()
		r.cache[req.NamespacedName]()
		r.Unlock()
	}

	// Ensure we keep track of the goroutine contexts associated with each env watcher
	r.Lock()
	r.cache[req.NamespacedName] = cancel
	r.Unlock()

	// Kick off goroutine for each env watch CRD
	go func() {
		// Not sure if we want to try to handle requeueing logic here since we return
		// below
		r.watcher(ctx, eWatch)
	}()

	return res, nil
}

// cleanupWatcher handles terminating any currently running goroutine associated with the env watcher resource
// as well as deleting the configmap associated with the env watcher.
func (r *EnvWatcherReconciler) cleanupWatcher(ctx context.Context, namespacedName types.NamespacedName) (err error) {
	r.Lock()
	if cancelfn, ok := r.cache[namespacedName]; ok {
		cancelfn()
	}
	r.Unlock()

	var cfgMap *corev1.ConfigMap
	if err = r.Get(ctx, namespacedName, cfgMap); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.Delete(ctx, cfgMap)
}

func (r *EnvWatcherReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lightwatchv1alpha1.EnvWatcher{}).
		Complete(r)
}

func (r *EnvWatcherReconciler) watcher(ctx context.Context, eWatch *lightwatchv1alpha1.EnvWatcher) error {
	// download file
	var (
		err            error
		namespacedName = types.NamespacedName{Name: eWatch.ObjectMeta.Name, Namespace: eWatch.ObjectMeta.Namespace}
		period         time.Duration
	)

	// discover configmap
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
				return err
			}
		} else {
			r.Log.Error(err, "failed to find configmap", "configmap", namespacedName)
			return err
		}
	}

	if period, err = time.ParseDuration(eWatch.Spec.Frequency); err != nil {
		r.Log.Error(err, "failed to parse frequency", "frequency", eWatch.Spec.Frequency)
		return err
	}

	// This means now() is after last check + 1h
	if time.Now().After(time.Unix(eWatch.Status.LastCheck, 0).Add(period)) {
		r.doStuff(ctx, eWatch, cfgMap)
	} else {
		// Wait difference between now and next iteration
		<-time.After(time.Now().Sub(time.Unix(eWatch.Status.LastCheck, 0).Add(period)))
		r.doStuff(ctx, eWatch, cfgMap)
	}

	// instantiate a new ticker
	watcherTicker := time.NewTicker(period)
	defer watcherTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-watcherTicker.C:
			r.doStuff(ctx, eWatch, cfgMap)
		}
	}
}

func (r *EnvWatcherReconciler) doStuff(ctx context.Context, eWatch *lightwatchv1alpha1.EnvWatcher, cfgMap *corev1.ConfigMap) {
	var (
		data []byte
		err  error
	)

	if data, err = downloadFile(eWatch.Spec.URL); err != nil {
		r.Log.Error(err, "failed to download file", "url", eWatch.Spec.URL)
		return
	}

	remoteChecksum := checksumData(data)

	eWatch.Status.LastCheck = time.Now().Unix()

	if eWatch.Status.Checksum != remoteChecksum {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			fields := strings.Split(scanner.Text(), "=")
			cfgMap.Data[fields[0]] = fields[1]
		}
		if err := scanner.Err(); err != nil {
			r.Log.Error(err, "failed to parse downloaded file", "contents", string(data))
			return
		}
	}

	eWatch.Status.Checksum = remoteChecksum

	if err = r.Update(ctx, cfgMap); err != nil {
		r.Log.Error(err, "failed to update configmap")
	}
	if err = r.Update(ctx, eWatch); err != nil {
		r.Log.Error(err, "failed to update env watcher")
	}
}

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

func checksumData(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
