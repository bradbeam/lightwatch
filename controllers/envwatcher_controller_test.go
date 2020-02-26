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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	lightwatchv1alpha1 "github.com/bradbeam/lightstream/api/v1alpha1"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type ControllerSuite struct {
	suite.Suite
	testEnv   *envtest.Environment
	k8sClient client.Client
	cfg       *rest.Config
	ts        *httptest.Server

	checksum         string
	watchName        string
	watchNS          string
	defaultFrequency string
	configLines      int
}

func TestControllerSuite(t *testing.T) {
	suite.Run(t, new(ControllerSuite))
}

func (suite *ControllerSuite) SetupTest() {
	var (
		cfg       *rest.Config
		err       error
		k8sClient client.Client
	)

	suite.defaultFrequency = "1s"

	suite.testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}

	cfg, err = suite.testEnv.Start()
	suite.Require().NoError(err)
	suite.Require().NotNil(cfg)
	suite.cfg = cfg

	err = lightwatchv1alpha1.AddToScheme(scheme.Scheme)
	suite.Require().NoError(err)

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	suite.Require().NoError(err)
	suite.Require().NotNil(k8sClient)
	suite.k8sClient = k8sClient

	// Verify CRD was added
	eWatch := &lightwatchv1alpha1.EnvWatcherList{}
	err = suite.k8sClient.List(context.Background(), eWatch)
	suite.Require().NoError(err)
	suite.Require().NotNil(eWatch)

	// Start up http server to serve assets
	suite.configLines = 10
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < suite.configLines; i++ {
			fmt.Fprintf(w, "key%d=value%d\n", i, i)
		}
	}))

	suite.ts = ts
	// Cheesed checksum ( sha256(output from above) )
	suite.checksum = "8f182d8440c7367aab0833cba67d7716d779e74a121f6c3540a57a8c7df1f3d3"

	suite.watchName = "something"
	suite.watchNS = "default"
}

func (suite *ControllerSuite) TearDownTest() {
	err := suite.testEnv.Stop()
	suite.Require().NoError(err)
	suite.ts.Close()
}

func (suite *ControllerSuite) TestCreateResource() {
	suite.createCRD(suite.defaultFrequency)
	suite.deleteCRD()
}

func (suite *ControllerSuite) TestControllerNewResource() {
	suite.createCRD(suite.defaultFrequency)
	defer suite.deleteCRD()

	ewReconciler := &EnvWatcherReconciler{
		Client: suite.k8sClient,
		Scheme: runtime.NewScheme(),
		Log:    ctrl.Log,
	}

	suite.createAndReconcile(ewReconciler)
}

func (suite *ControllerSuite) TestControllerDeleteResource() {
	suite.createCRD(suite.defaultFrequency)
	defer suite.deleteCRD()

	ewReconciler := &EnvWatcherReconciler{
		Client: suite.k8sClient,
		Scheme: runtime.NewScheme(),
		Log:    ctrl.Log,
	}

	suite.createAndReconcile(ewReconciler)
	suite.deleteAndReconcile(ewReconciler)
}

func (suite *ControllerSuite) TestControllerRecreateConfigmap() {
	suite.createCRD(suite.defaultFrequency)
	defer suite.deleteCRD()

	ewReconciler := &EnvWatcherReconciler{
		Client: suite.k8sClient,
		Scheme: runtime.NewScheme(),
		Log:    ctrl.Log,
	}

	suite.createAndReconcile(ewReconciler)

	// test deletion of configmap; this should get recreated during the next run
	cfgMap := &corev1.ConfigMap{}
	err := suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, cfgMap)
	suite.Assert().NoError(err)
	err = suite.k8sClient.Delete(context.Background(), cfgMap)
	suite.Assert().NoError(err)

	// wait frequency
	time.Sleep(time.Second)

	// Wait for configmap to be created
	for i := 0; i < 10; i++ {
		if err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, cfgMap); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	suite.Assert().NoError(err)
	// Verify configmap name matches
	suite.Assert().Equal(suite.watchName, cfgMap.ObjectMeta.Name)
	// Verify we have expected configmap.data length
	suite.Assert().Len(cfgMap.Data, suite.configLines)
}

func (suite *ControllerSuite) TestControllerRunner() {
	suite.createCRD(suite.defaultFrequency)
	defer suite.deleteCRD()

	ewReconciler := &EnvWatcherReconciler{
		Client: suite.k8sClient,
		Scheme: runtime.NewScheme(),
		Log:    ctrl.Log,
	}

	suite.createAndReconcile(ewReconciler)

	// Verify EnvWatcher status was updated correctly
	eWatcher := &lightwatchv1alpha1.EnvWatcher{}
	err := suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)

	prevLastCheck := eWatcher.Status.LastCheck

	// Wait for an update
	time.Sleep(2 * time.Second)

	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)
	suite.Assert().GreaterOrEqual(eWatcher.Status.LastCheck, prevLastCheck)

	suite.verifyConfigmap()
}

func (suite *ControllerSuite) TestControllerUpdateResource() {
	suite.createCRD(suite.defaultFrequency)
	defer suite.deleteCRD()

	ewReconciler := &EnvWatcherReconciler{
		Client: suite.k8sClient,
		Scheme: runtime.NewScheme(),
		Log:    ctrl.Log,
	}

	suite.createAndReconcile(ewReconciler)

	// Get previous last check
	eWatcher := &lightwatchv1alpha1.EnvWatcher{}
	err := suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)

	prevWatcher := *eWatcher

	// Update CRD to change frequency from 1s to 5s
	suite.createCRD("5s")

	// Verify CRD was updated
	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)
	suite.Assert().Equal("5s", eWatcher.Spec.Frequency)
	suite.verifyConfigmap()

	// Wait for an update ( new interval + some cushion )
	time.Sleep(7 * time.Second)

	// Compare old watcher to new watcher
	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)
	suite.Assert().GreaterOrEqual(eWatcher.Status.LastCheck, prevWatcher.Status.LastCheck)
	// Generation should have only incremented by one ( we changed the value )
	suite.Assert().Equal(eWatcher.ObjectMeta.Generation, prevWatcher.ObjectMeta.Generation+1)

	suite.verifyConfigmap()
}

func (suite *ControllerSuite) TestDownloadFile() {
	data, err := downloadFile(suite.ts.URL)
	suite.Assert().NoError(err)

	checksum := checksumData(data)
	suite.Assert().Equal(suite.checksum, checksum)
}

// Helper funcs
func (suite *ControllerSuite) createCRD(frequency string) {
	var err error
	eWatch := &lightwatchv1alpha1.EnvWatcher{}

	if err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatch); err != nil {
		eWatch = &lightwatchv1alpha1.EnvWatcher{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: suite.watchNS,
				Name:      suite.watchName,
			},
			Spec: lightwatchv1alpha1.EnvWatcherSpec{
				URL:       suite.ts.URL,
				Frequency: frequency,
			},
		}
		err = suite.k8sClient.Create(context.Background(), eWatch)
	} else {
		eWatch.Spec.Frequency = frequency
		err = suite.k8sClient.Update(context.Background(), eWatch)
	}
	suite.Assert().NoError(err)

	eWatch = &lightwatchv1alpha1.EnvWatcher{}

	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatch)

	suite.Assert().NoError(err)
	suite.Assert().Equal(eWatch.Spec.URL, suite.ts.URL)
}

func (suite *ControllerSuite) deleteCRD() {
	var err error
	eWatch := &lightwatchv1alpha1.EnvWatcher{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: suite.watchNS,
			Name:      suite.watchName,
		},
	}

	err = suite.k8sClient.Delete(context.Background(), eWatch)
	switch {
	case errors.IsNotFound(err):
	// Ignore if resource doesnt exist
	default:
		suite.Assert().NoError(err)
	}
}

func (suite *ControllerSuite) createCM() {
	var err error
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: suite.watchNS,
			Name:      suite.watchName,
		},
		Data: map[string]string{"test1": "test"},
	}
	err = suite.k8sClient.Create(context.Background(), cm)
	suite.Assert().NoError(err)

	cm = &corev1.ConfigMap{}

	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, cm)

	suite.Assert().NoError(err)
	suite.Assert().Equal(cm.Data, map[string]string{"test1": "test"})
}

func (suite *ControllerSuite) deleteCM() {
	var err error
	eWatch := &lightwatchv1alpha1.EnvWatcher{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: suite.watchNS,
			Name:      suite.watchName,
		},
	}
	err = suite.k8sClient.Delete(context.Background(), eWatch)
	suite.Assert().NoError(err)
}

func (suite *ControllerSuite) verifyConfigmap() {
	var err error
	cfgMap := &corev1.ConfigMap{}

	// Wait for configmap to be created
	for i := 0; i < 10; i++ {
		if err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, cfgMap); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	suite.Assert().NoError(err)
	// Verify configmap name matches
	suite.Assert().Equal(suite.watchName, cfgMap.ObjectMeta.Name)
	// Verify we have expected configmap.data length
	suite.Assert().Len(cfgMap.Data, suite.configLines)

}
func (suite *ControllerSuite) createAndReconcile(ewReconciler *EnvWatcherReconciler) {
	testReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: suite.watchName, Namespace: suite.watchNS}}

	// Creation
	_, err := ewReconciler.Reconcile(testReq)
	suite.Assert().NoError(err)

	suite.verifyConfigmap()

	// Verify EnvWatcher status was updated correctly
	eWatcher := &lightwatchv1alpha1.EnvWatcher{}
	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)
	// Verify checksums match
	suite.Assert().Equal(suite.checksum, eWatcher.Status.Checksum)
	// Verify the last check timestamp was recent ( within last 5 seconds )
	suite.Assert().LessOrEqual(time.Now().Sub(time.Unix(eWatcher.Status.LastCheck, 0)).Seconds(), float64(5))
}

func (suite *ControllerSuite) deleteAndReconcile(ewReconciler *EnvWatcherReconciler) {
	testReq := ctrl.Request{NamespacedName: types.NamespacedName{Name: suite.watchName, Namespace: suite.watchNS}}

	// delete crd, this should remove crd and configmap
	// delete crd
	eWatcher := &lightwatchv1alpha1.EnvWatcher{}
	err := suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, eWatcher)
	suite.Assert().NoError(err)
	err = suite.k8sClient.Delete(context.Background(), eWatcher)
	suite.Assert().NoError(err)

	// Send event for CRD; this should trigger the downstream cleanup
	_, err = ewReconciler.Reconcile(testReq)
	suite.Assert().NoError(err)

	time.Sleep(time.Second)

	// check for deleted cfgmap
	cfgMap := &corev1.ConfigMap{}
	err = suite.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: suite.watchNS, Name: suite.watchName}, cfgMap)
	suite.Assert().Error(err)
}
