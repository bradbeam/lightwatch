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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.
// Important: Run "make" to regenerate code after modifying this file

// EnvWatcherSpec defines the desired state of EnvWatcher
type EnvWatcherSpec struct {
	// URL specifies the location of the remote file to fetch. This file
	// should contain key=value pairs to be used for environment variables.
	URL string `json:"url"`
	// Frequency specifies the hourly interval to check for updates to
	// the file.
	Frequency string `json:"frequency,omitempty"`
}

// EnvWatcherStatus defines the observed state of EnvWatcher
type EnvWatcherStatus struct {

	// LastCheck is a timestamp representing the last time this resource
	// was fetched/checked.
	LastCheck int64  `json:"lastcheck,omitempty"`
	Checksum  string `json:"checksum,omitempty"`
}

// +kubebuilder:object:root=true

// EnvWatcher is the Schema for the envwatchers API
type EnvWatcher struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvWatcherSpec   `json:"spec,omitempty"`
	Status EnvWatcherStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvWatcherList contains a list of EnvWatcher
type EnvWatcherList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EnvWatcher `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EnvWatcher{}, &EnvWatcherList{})
}
