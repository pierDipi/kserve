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
	"fmt"
	"log/slog"
	"net/url"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// GatewayTarget identifies a Gateway and optionally a specific listener section.
type GatewayTarget struct {
	Name        string
	Namespace   string
	SectionName string
}

// HTTPRouteDiscovery discovers backends by listing HTTPRoutes that are accepted
// by a target Gateway. This handles both controller-managed and externally
// managed (BYO) HTTPRoutes. For managed routes, the owning LLMInferenceService
// is resolved via owner references to obtain the canonical service URL. For BYO
// routes, backend URLs are constructed from the route's backendRefs.
type HTTPRouteDiscovery struct {
	client    client.Client
	gateway   GatewayTarget
	namespace string
}

type HTTPRouteDiscoveryOption func(*HTTPRouteDiscovery)

func HTTPRouteInNamespace(ns string) HTTPRouteDiscoveryOption {
	return func(d *HTTPRouteDiscovery) {
		d.namespace = ns
	}
}

func NewHTTPRouteDiscovery(c client.Client, gw GatewayTarget, opts ...HTTPRouteDiscoveryOption) *HTTPRouteDiscovery {
	d := &HTTPRouteDiscovery{
		client:  c,
		gateway: gw,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *HTTPRouteDiscovery) Discover(ctx context.Context) ([]Backend, error) {
	var routeList gwapiv1.HTTPRouteList

	listOpts := []client.ListOption{}
	if d.namespace != "" {
		listOpts = append(listOpts, client.InNamespace(d.namespace))
	}

	if err := d.client.List(ctx, &routeList, listOpts...); err != nil {
		return nil, fmt.Errorf("listing HTTPRoutes: %w", err)
	}

	seen := map[string]bool{}
	var backends []Backend

	for i := range routeList.Items {
		route := &routeList.Items[i]

		if !d.routeTargetsGateway(route) {
			continue
		}

		if !d.routeAccepted(route) {
			continue
		}

		// Controller-managed routes have an owner reference to the LLMInferenceService.
		if b, ok := d.resolveFromOwner(ctx, route, seen); ok {
			backends = append(backends, b...)
			continue
		}

		// BYO routes: extract backends directly from backendRefs.
		backends = append(backends, d.resolveFromBackendRefs(route, seen)...)
	}

	return backends, nil
}

func (d *HTTPRouteDiscovery) routeTargetsGateway(route *gwapiv1.HTTPRoute) bool {
	for _, ref := range route.Spec.ParentRefs {
		if !parentRefMatchesGateway(ref, route.Namespace, d.gateway) {
			continue
		}
		return true
	}
	return false
}

func parentRefMatchesGateway(ref gwapiv1.ParentReference, routeNamespace string, gw GatewayTarget) bool {
	if ref.Group != nil && *ref.Group != gwapiv1.GroupName {
		return false
	}
	if ref.Kind != nil && *ref.Kind != "Gateway" {
		return false
	}

	if string(ref.Name) != gw.Name {
		return false
	}

	refNS := routeNamespace
	if ref.Namespace != nil {
		refNS = string(*ref.Namespace)
	}
	if refNS != gw.Namespace {
		return false
	}

	if gw.SectionName != "" {
		if ref.SectionName == nil || string(*ref.SectionName) != gw.SectionName {
			return false
		}
	}

	return true
}

func (d *HTTPRouteDiscovery) routeAccepted(route *gwapiv1.HTTPRoute) bool {
	for _, parentStatus := range route.Status.Parents {
		if !parentRefMatchesGateway(parentStatus.ParentRef, route.Namespace, d.gateway) {
			continue
		}

		accepted := false
		resolvedRefs := false
		for _, cond := range parentStatus.Conditions {
			if cond.ObservedGeneration > 0 && cond.ObservedGeneration < route.Generation {
				continue
			}
			if cond.Type == string(gwapiv1.RouteConditionAccepted) && cond.Status == metav1.ConditionTrue {
				accepted = true
			}
			if cond.Type == string(gwapiv1.RouteConditionResolvedRefs) && cond.Status == metav1.ConditionTrue {
				resolvedRefs = true
			}
		}

		if accepted && resolvedRefs {
			return true
		}
	}
	return false
}

func (d *HTTPRouteDiscovery) resolveFromOwner(ctx context.Context, route *gwapiv1.HTTPRoute, seen map[string]bool) ([]Backend, bool) {
	for _, ownerRef := range route.OwnerReferences {
		if ownerRef.APIVersion != v1alpha2.SchemeGroupVersion.String() || ownerRef.Kind != "LLMInferenceService" {
			continue
		}

		key := route.Namespace + "/" + ownerRef.Name
		if seen[key] {
			return nil, true
		}

		var llmSvc v1alpha2.LLMInferenceService
		if err := d.client.Get(ctx, types.NamespacedName{
			Namespace: route.Namespace,
			Name:      ownerRef.Name,
		}, &llmSvc); err != nil {
			slog.Warn("failed to resolve LLMInferenceService from owner ref",
				"route", route.Name, "owner", ownerRef.Name, "error", err)
			return nil, true
		}

		endpointURL := llmSvcURL(&llmSvc)
		if endpointURL == "" {
			slog.Warn("LLMInferenceService has no URL", "name", llmSvc.Name, "namespace", llmSvc.Namespace)
			return nil, true
		}

		parsedURL, err := url.Parse(endpointURL)
		if err != nil {
			slog.Warn("invalid LLMInferenceService URL", "name", llmSvc.Name, "url", endpointURL, "error", err)
			return nil, true
		}

		ready := false
		if cond := llmSvc.GetStatus().GetCondition(apis.ConditionReady); cond != nil && cond.IsTrue() {
			ready = true
		}

		seen[key] = true
		return []Backend{{
			Name:      llmSvc.Name,
			Namespace: llmSvc.Namespace,
			URL:       parsedURL,
			Labels:    llmSvc.Labels,
			Ready:     ready,
		}}, true
	}
	return nil, false
}

func llmSvcURL(svc *v1alpha2.LLMInferenceService) string {
	if svc.Status.URL != nil {
		return svc.Status.URL.String()
	}
	if len(svc.Status.Addresses) > 0 && svc.Status.Addresses[0].URL != nil {
		return svc.Status.Addresses[0].URL.String()
	}
	return ""
}

func (d *HTTPRouteDiscovery) resolveFromBackendRefs(route *gwapiv1.HTTPRoute, seen map[string]bool) []Backend {
	var backends []Backend
	for _, rule := range route.Spec.Rules {
		for _, ref := range rule.BackendRefs {
			if ref.Group != nil && *ref.Group != "" {
				continue
			}
			if ref.Kind != nil && *ref.Kind != "Service" {
				continue
			}

			ns := route.Namespace
			if ref.Namespace != nil {
				ns = string(*ref.Namespace)
			}
			name := string(ref.Name)
			key := ns + "/" + name

			if seen[key] {
				continue
			}

			port := int32(8000)
			if ref.Port != nil {
				port = int32(*ref.Port)
			}

			u := &url.URL{
				Scheme: "http",
				Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", name, ns, port),
			}

			seen[key] = true
			backends = append(backends, Backend{
				Name:      name,
				Namespace: ns,
				URL:       u,
				Ready:     true,
			})
		}
	}
	return backends
}
