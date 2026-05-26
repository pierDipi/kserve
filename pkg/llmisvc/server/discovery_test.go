/*
Copyright 2025 The KServe Authors.

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

package server

import (
	"context"
	"net/url"
	"testing"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStaticDiscovery(t *testing.T) {
	u, _ := url.Parse("http://example.com")
	backends := []Backend{
		{Name: "b1", Namespace: "ns1", URL: u, Ready: true},
		{Name: "b2", Namespace: "ns2", URL: u, Ready: false},
	}

	d := NewStaticDiscovery(backends)
	result, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(result))
	}
	if result[0].Name != "b1" {
		t.Fatalf("expected b1, got %s", result[0].Name)
	}
	if result[1].Name != "b2" {
		t.Fatalf("expected b2, got %s", result[1].Name)
	}
}

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha2.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func TestKubernetesDiscoveryWithStatusURL(t *testing.T) {
	scheme := testScheme()

	statusURL, _ := apis.ParseURL("http://llm-svc.default.svc.cluster.local")

	list := &v1alpha2.LLMInferenceServiceList{
		Items: []v1alpha2.LLMInferenceService{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llm-svc",
					Namespace: "default",
					Labels:    map[string]string{"app": "llm"},
				},
				Status: v1alpha2.LLMInferenceServiceStatus{
					URL: statusURL,
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{
							{
								Type:   "Ready",
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(list).
		Build()

	d := NewKubernetesDiscovery(k8sClient)
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].Name != "llm-svc" {
		t.Fatalf("expected llm-svc, got %s", backends[0].Name)
	}
	if backends[0].Namespace != "default" {
		t.Fatalf("expected default namespace, got %s", backends[0].Namespace)
	}
	if !backends[0].Ready {
		t.Fatal("expected backend to be ready")
	}
	if backends[0].URL.String() != "http://llm-svc.default.svc.cluster.local" {
		t.Fatalf("expected URL http://llm-svc.default.svc.cluster.local, got %s", backends[0].URL.String())
	}
}

func TestKubernetesDiscoveryFallbackToAddresses(t *testing.T) {
	scheme := testScheme()

	addrURL, _ := apis.ParseURL("http://llm-addr.default.svc.cluster.local")

	list := &v1alpha2.LLMInferenceServiceList{
		Items: []v1alpha2.LLMInferenceService{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "llm-addr",
					Namespace: "default",
				},
				Status: v1alpha2.LLMInferenceServiceStatus{
					Addresses: []v1alpha2.SourcedAddress{
						{
							Addressable: duckv1.Addressable{
								URL: addrURL,
							},
						},
					},
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{
							{
								Type:   "Ready",
								Status: corev1.ConditionFalse,
							},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(list).
		Build()

	d := NewKubernetesDiscovery(k8sClient)
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].Ready {
		t.Fatal("expected backend to not be ready")
	}
}

func TestKubernetesDiscoverySkipsNoURL(t *testing.T) {
	scheme := testScheme()

	list := &v1alpha2.LLMInferenceServiceList{
		Items: []v1alpha2.LLMInferenceService{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-url",
					Namespace: "default",
				},
				Status: v1alpha2.LLMInferenceServiceStatus{},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(list).
		Build()

	d := NewKubernetesDiscovery(k8sClient)
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 0 {
		t.Fatalf("expected 0 backends, got %d", len(backends))
	}
}

func TestKubernetesDiscoveryWithNamespace(t *testing.T) {
	scheme := testScheme()

	url1, _ := apis.ParseURL("http://svc1.ns1.svc.cluster.local")
	url2, _ := apis.ParseURL("http://svc2.ns2.svc.cluster.local")

	list := &v1alpha2.LLMInferenceServiceList{
		Items: []v1alpha2.LLMInferenceService{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "svc1", Namespace: "ns1"},
				Status: v1alpha2.LLMInferenceServiceStatus{
					URL: url1,
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{Type: "Ready", Status: corev1.ConditionTrue}},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "svc2", Namespace: "ns2"},
				Status: v1alpha2.LLMInferenceServiceStatus{
					URL: url2,
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{Type: "Ready", Status: corev1.ConditionTrue}},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithLists(list).
		Build()

	d := NewKubernetesDiscovery(k8sClient, WithNamespace("ns1"))
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend (ns1 only), got %d", len(backends))
	}
	if backends[0].Name != "svc1" {
		t.Fatalf("expected svc1, got %s", backends[0].Name)
	}
}
