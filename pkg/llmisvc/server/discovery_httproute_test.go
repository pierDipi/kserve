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
	"testing"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func httpRouteTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha2.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = gwapiv1.Install(s)
	return s
}

func acceptedParentStatus(gwName, gwNamespace string) gwapiv1.RouteParentStatus {
	return gwapiv1.RouteParentStatus{
		ParentRef: gwapiv1.ParentReference{
			Group:     ptr.To(gwapiv1.Group(gwapiv1.GroupName)),
			Kind:      ptr.To(gwapiv1.Kind("Gateway")),
			Name:      gwapiv1.ObjectName(gwName),
			Namespace: ptr.To(gwapiv1.Namespace(gwNamespace)),
		},
		Conditions: []metav1.Condition{
			{Type: string(gwapiv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
			{Type: string(gwapiv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue},
		},
	}
}

func TestHTTPRouteDiscoveryManagedRoute(t *testing.T) {
	scheme := httpRouteTestScheme()

	statusURL, _ := apis.ParseURL("http://llm-svc.default.svc.cluster.local")

	llmSvc := &v1alpha2.LLMInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "llm-svc",
			Namespace: "default",
			UID:       "uid-1",
			Labels:    map[string]string{"app": "llm"},
		},
		Status: v1alpha2.LLMInferenceServiceStatus{
			URL: statusURL,
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: "Ready", Status: corev1.ConditionTrue},
				},
			},
		},
	}

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "llm-svc-kserve-route",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha2.SchemeGroupVersion.String(),
					Kind:       "LLMInferenceService",
					Name:       "llm-svc",
					UID:        "uid-1",
				},
			},
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Group:     ptr.To(gwapiv1.Group(gwapiv1.GroupName)),
						Kind:      ptr.To(gwapiv1.Kind("Gateway")),
						Name:      "my-gateway",
						Namespace: ptr.To(gwapiv1.Namespace("infra")),
					},
				},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					acceptedParentStatus("my-gateway", "infra"),
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(llmSvc, route).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "my-gateway", Namespace: "infra"})
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
	if backends[0].URL.String() != "http://llm-svc.default.svc.cluster.local" {
		t.Fatalf("expected URL http://llm-svc.default.svc.cluster.local, got %s", backends[0].URL.String())
	}
	if !backends[0].Ready {
		t.Fatal("expected backend to be ready")
	}
}

func TestHTTPRouteDiscoveryBYORoute(t *testing.T) {
	scheme := httpRouteTestScheme()

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-custom-route",
			Namespace: "team-a",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Group:     ptr.To(gwapiv1.Group(gwapiv1.GroupName)),
						Kind:      ptr.To(gwapiv1.Kind("Gateway")),
						Name:      "shared-gw",
						Namespace: ptr.To(gwapiv1.Namespace("infra")),
					},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{
					BackendRefs: []gwapiv1.HTTPBackendRef{
						{
							BackendRef: gwapiv1.BackendRef{
								BackendObjectReference: gwapiv1.BackendObjectReference{
									Group: ptr.To(gwapiv1.Group("")),
									Kind:  ptr.To(gwapiv1.Kind("Service")),
									Name:  "my-model-svc",
									Port:  ptr.To(gwapiv1.PortNumber(8080)),
								},
							},
						},
					},
				},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					acceptedParentStatus("shared-gw", "infra"),
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "shared-gw", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(backends))
	}
	if backends[0].Name != "my-model-svc" {
		t.Fatalf("expected my-model-svc, got %s", backends[0].Name)
	}
	if backends[0].URL.String() != "http://my-model-svc.team-a.svc.cluster.local:8080" {
		t.Fatalf("expected http://my-model-svc.team-a.svc.cluster.local:8080, got %s", backends[0].URL.String())
	}
}

func TestHTTPRouteDiscoveryFiltersUnacceptedRoutes(t *testing.T) {
	scheme := httpRouteTestScheme()

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "not-accepted",
			Namespace: "default",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Group:     ptr.To(gwapiv1.Group(gwapiv1.GroupName)),
						Kind:      ptr.To(gwapiv1.Kind("Gateway")),
						Name:      "my-gw",
						Namespace: ptr.To(gwapiv1.Namespace("infra")),
					},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{
					BackendRefs: []gwapiv1.HTTPBackendRef{
						{
							BackendRef: gwapiv1.BackendRef{
								BackendObjectReference: gwapiv1.BackendObjectReference{
									Name: "svc",
									Port: ptr.To(gwapiv1.PortNumber(8000)),
								},
							},
						},
					},
				},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					{
						ParentRef: gwapiv1.ParentReference{
							Group:     ptr.To(gwapiv1.Group(gwapiv1.GroupName)),
							Kind:      ptr.To(gwapiv1.Kind("Gateway")),
							Name:      "my-gw",
							Namespace: ptr.To(gwapiv1.Namespace("infra")),
						},
						Conditions: []metav1.Condition{
							{Type: string(gwapiv1.RouteConditionAccepted), Status: metav1.ConditionFalse},
							{Type: string(gwapiv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "my-gw", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 0 {
		t.Fatalf("expected 0 backends for unaccepted route, got %d", len(backends))
	}
}

func TestHTTPRouteDiscoveryFiltersByGateway(t *testing.T) {
	scheme := httpRouteTestScheme()

	routeForGW1 := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-gw1",
			Namespace: "default",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gateway-1", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{BackendRefs: []gwapiv1.HTTPBackendRef{
					{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name: "svc-1", Port: ptr.To(gwapiv1.PortNumber(8000)),
					}}},
				}},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					acceptedParentStatus("gateway-1", "infra"),
				},
			},
		},
	}

	routeForGW2 := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-gw2",
			Namespace: "default",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gateway-2", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{BackendRefs: []gwapiv1.HTTPBackendRef{
					{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name: "svc-2", Port: ptr.To(gwapiv1.PortNumber(8000)),
					}}},
				}},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					acceptedParentStatus("gateway-2", "infra"),
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(routeForGW1, routeForGW2).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "gateway-1", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend for gateway-1, got %d", len(backends))
	}
	if backends[0].Name != "svc-1" {
		t.Fatalf("expected svc-1, got %s", backends[0].Name)
	}
}

func TestHTTPRouteDiscoverySectionNameFiltering(t *testing.T) {
	scheme := httpRouteTestScheme()

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route-section",
			Namespace: "default",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Name:        "my-gw",
						Namespace:   ptr.To(gwapiv1.Namespace("infra")),
						SectionName: ptr.To(gwapiv1.SectionName("https-listener")),
					},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{BackendRefs: []gwapiv1.HTTPBackendRef{
					{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name: "svc", Port: ptr.To(gwapiv1.PortNumber(8000)),
					}}},
				}},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					{
						ParentRef: gwapiv1.ParentReference{
							Name:        "my-gw",
							Namespace:   ptr.To(gwapiv1.Namespace("infra")),
							SectionName: ptr.To(gwapiv1.SectionName("https-listener")),
						},
						Conditions: []metav1.Condition{
							{Type: string(gwapiv1.RouteConditionAccepted), Status: metav1.ConditionTrue},
							{Type: string(gwapiv1.RouteConditionResolvedRefs), Status: metav1.ConditionTrue},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		Build()

	// Should find route when matching section name
	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "my-gw", Namespace: "infra", SectionName: "https-listener"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 backend for matching section, got %d", len(backends))
	}

	// Should NOT find route when section name doesn't match
	d2 := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "my-gw", Namespace: "infra", SectionName: "other-listener"})
	backends2, err := d2.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends2) != 0 {
		t.Fatalf("expected 0 backends for non-matching section, got %d", len(backends2))
	}

	// Should find route when no section name specified (matches all)
	d3 := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "my-gw", Namespace: "infra"})
	backends3, err := d3.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends3) != 1 {
		t.Fatalf("expected 1 backend with no section filter, got %d", len(backends3))
	}
}

func TestHTTPRouteDiscoveryDeduplicates(t *testing.T) {
	scheme := httpRouteTestScheme()

	statusURL, _ := apis.ParseURL("http://llm-svc.default.svc.cluster.local")

	llmSvc := &v1alpha2.LLMInferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "llm-svc",
			Namespace: "default",
			UID:       "uid-1",
		},
		Status: v1alpha2.LLMInferenceServiceStatus{
			URL: statusURL,
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: "Ready", Status: corev1.ConditionTrue},
				},
			},
		},
	}

	ownerRef := metav1.OwnerReference{
		APIVersion: v1alpha2.SchemeGroupVersion.String(),
		Kind:       "LLMInferenceService",
		Name:       "llm-svc",
		UID:        "uid-1",
	}

	route1 := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "route-1",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gw", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{acceptedParentStatus("gw", "infra")},
			},
		},
	}

	route2 := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "route-2",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gw", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{acceptedParentStatus("gw", "infra")},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(llmSvc, route1, route2).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "gw", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 1 {
		t.Fatalf("expected 1 deduplicated backend, got %d", len(backends))
	}
}

func TestHTTPRouteDiscoveryStaleConditions(t *testing.T) {
	scheme := httpRouteTestScheme()

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "stale-route",
			Namespace:  "default",
			Generation: 5,
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gw", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{BackendRefs: []gwapiv1.HTTPBackendRef{
					{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Name: "svc", Port: ptr.To(gwapiv1.PortNumber(8000)),
					}}},
				}},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					{
						ParentRef: gwapiv1.ParentReference{
							Name:      "gw",
							Namespace: ptr.To(gwapiv1.Namespace("infra")),
						},
						Conditions: []metav1.Condition{
							{
								Type:               string(gwapiv1.RouteConditionAccepted),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 3,
							},
							{
								Type:               string(gwapiv1.RouteConditionResolvedRefs),
								Status:             metav1.ConditionTrue,
								ObservedGeneration: 3,
							},
						},
					},
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "gw", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 0 {
		t.Fatalf("expected 0 backends for stale conditions, got %d", len(backends))
	}
}

func TestHTTPRouteDiscoverySkipsNonServiceBackendRefs(t *testing.T) {
	scheme := httpRouteTestScheme()

	route := &gwapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-route",
			Namespace: "default",
		},
		Spec: gwapiv1.HTTPRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{Name: "gw", Namespace: ptr.To(gwapiv1.Namespace("infra"))},
				},
			},
			Rules: []gwapiv1.HTTPRouteRule{
				{BackendRefs: []gwapiv1.HTTPBackendRef{
					{BackendRef: gwapiv1.BackendRef{BackendObjectReference: gwapiv1.BackendObjectReference{
						Group: ptr.To(gwapiv1.Group("inference.networking.k8s.io")),
						Kind:  ptr.To(gwapiv1.Kind("InferencePool")),
						Name:  "pool",
						Port:  ptr.To(gwapiv1.PortNumber(8000)),
					}}},
				}},
			},
		},
		Status: gwapiv1.HTTPRouteStatus{
			RouteStatus: gwapiv1.RouteStatus{
				Parents: []gwapiv1.RouteParentStatus{
					acceptedParentStatus("gw", "infra"),
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(route).
		Build()

	d := NewHTTPRouteDiscovery(k8sClient, GatewayTarget{Name: "gw", Namespace: "infra"})
	backends, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backends) != 0 {
		t.Fatalf("expected 0 backends for InferencePool backendRef, got %d", len(backends))
	}
}
