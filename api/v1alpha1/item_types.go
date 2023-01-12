/*
Copyright 2023.

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

const (
	PhaseNewlyCreated             = ""
	PhaseReachingContextConsensus = "ReachingContextConsensus"
	PhaseResolving                = "Resolving"
	PhaseUnpacking                = "Unpacking"
	PhaseInstalling               = "Installing"
	PhaseInstalled                = "Installed"
)

// ItemSpec defines the desired state of Item
type ItemSpec struct {
	Dependencies []string `json:"dependencies,omitempty"`
}

// ItemStatus defines the observed state of Item
type ItemStatus struct {
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name=Phase,type=string,JSONPath=`.status.phase`
//+kubebuilder:printcolumn:name=Age,type=date,JSONPath=`.metadata.creationTimestamp`

// Item is the Schema for the items API
type Item struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ItemSpec   `json:"spec,omitempty"`
	Status ItemStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ItemList contains a list of Item
type ItemList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Item `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Item{}, &ItemList{})
}
