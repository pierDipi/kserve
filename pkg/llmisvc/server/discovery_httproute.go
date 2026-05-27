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
	"net/http"
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

type gatewayTargetKey struct{}

// GatewayTargetFromContext retrieves a GatewayTarget from the context, if present.
func GatewayTargetFromContext(ctx context.Context) (GatewayTarget, bool) {
	gt, ok := ctx.Value(gatewayTargetKey{}).(GatewayTarget)
	return gt, ok
}

// ContextWithGatewayTarget returns a new context carrying the given GatewayTarget.
func ContextWithGatewayTarget(ctx context.Context, gt GatewayTarget) context.Context {
	return context.WithValue(ctx, gatewayTargetKey{}, gt)
}

const (
	HeaderGatewayName        = "X-Gateway-Name"
	HeaderGatewayNamespace   = "X-Gateway-Namespace"
	HeaderGatewaySectionName = "X-Gateway-Section-Name"
)

// GatewayHeaderMiddleware extracts gateway target information from request
// headers and injects it into the request context. This allows an auth layer
// to dynamically select the gateway for discovery. If X-Gateway-Name and
// X-Gateway-Namespace are both present, they override any static configuration.
func GatewayHeaderMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Header.Get(HeaderGatewayName)
		ns := r.Header.Get(HeaderGatewayNamespace)
		if name != "" && ns != "" {
			gt := GatewayTarget{
				Name:        name,
				Namespace:   ns,
				SectionName: r.Header.Get(HeaderGatewaySectionName),
			}
			r = r.WithContext(ContextWithGatewayTarget(r.Context(), gt))
		}
		next.ServeHTTP(w, r)
	})
}

// HTTPRouteDiscovery discovers backends by listing HTTPRoutes that are accepted
// by a target Gateway. Requests to backends are routed through the Gateway
// using the route's hostname as the Host header. For managed routes, the owning
// LLMInferenceService is resolved via owner references. For BYO routes, the
// hostname is taken from the route's spec.
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
	gw := d.gateway
	if ctxGW, ok := GatewayTargetFromContext(ctx); ok {
		gw = ctxGW
	}

	gwURL, err := d.resolveGatewayURL(ctx, gw)
	if err != nil {
		return nil, fmt.Errorf("resolving gateway address: %w", err)
	}

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

		if !routeTargetsGateway(route, gw) {
			continue
		}

		if !routeAccepted(route, gw) {
			continue
		}

		if b, ok := d.resolveFromOwner(ctx, route, gwURL, seen); ok {
			backends = append(backends, b...)
			continue
		}

		backends = append(backends, d.resolveFromBackendRefs(route, gwURL, seen)...)
	}

	return backends, nil
}

// resolveGatewayURL fetches the Gateway resource and returns a URL built from
// its status address and the matching listener port.
func (d *HTTPRouteDiscovery) resolveGatewayURL(ctx context.Context, gw GatewayTarget) (*url.URL, error) {
	var gateway gwapiv1.Gateway
	if err := d.client.Get(ctx, types.NamespacedName{
		Name:      gw.Name,
		Namespace: gw.Namespace,
	}, &gateway); err != nil {
		return nil, fmt.Errorf("getting Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}

	if len(gateway.Status.Addresses) == 0 {
		return nil, fmt.Errorf("gateway %s/%s has no addresses", gw.Namespace, gw.Name)
	}

	address := gateway.Status.Addresses[0].Value

	port, scheme := listenerPortAndScheme(&gateway, gw.SectionName)

	return &url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", address, port),
	}, nil
}

func listenerPortAndScheme(gw *gwapiv1.Gateway, sectionName string) (int32, string) {
	for _, l := range gw.Spec.Listeners {
		if sectionName != "" && string(l.Name) != sectionName {
			continue
		}
		scheme := "http"
		if l.Protocol == gwapiv1.HTTPSProtocolType || l.Protocol == gwapiv1.TLSProtocolType {
			scheme = "https"
		}
		return int32(l.Port), scheme
	}
	return 80, "http"
}

func routeTargetsGateway(route *gwapiv1.HTTPRoute, gw GatewayTarget) bool {
	for _, ref := range route.Spec.ParentRefs {
		if !parentRefMatchesGateway(ref, route.Namespace, gw) {
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

func routeAccepted(route *gwapiv1.HTTPRoute, gw GatewayTarget) bool {
	for _, parentStatus := range route.Status.Parents {
		if !parentRefMatchesGateway(parentStatus.ParentRef, route.Namespace, gw) {
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

func (d *HTTPRouteDiscovery) resolveFromOwner(ctx context.Context, route *gwapiv1.HTTPRoute, gwURL *url.URL, seen map[string]bool) ([]Backend, bool) {
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

		host := llmSvcHost(&llmSvc)
		if host == "" {
			// Fall back to route hostnames.
			if len(route.Spec.Hostnames) > 0 {
				host = string(route.Spec.Hostnames[0])
			}
		}
		if host == "" {
			slog.Warn("LLMInferenceService has no hostname", "name", llmSvc.Name, "namespace", llmSvc.Namespace)
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
			URL:       gwURL,
			Host:      host,
			Labels:    llmSvc.Labels,
			Ready:     ready,
		}}, true
	}
	return nil, false
}

// llmSvcHost extracts the hostname from the LLMInferenceService status URL.
func llmSvcHost(svc *v1alpha2.LLMInferenceService) string {
	if svc.Status.URL != nil {
		return svc.Status.URL.Host
	}
	if len(svc.Status.Addresses) > 0 && svc.Status.Addresses[0].URL != nil {
		return svc.Status.Addresses[0].URL.Host
	}
	return ""
}

func (d *HTTPRouteDiscovery) resolveFromBackendRefs(route *gwapiv1.HTTPRoute, gwURL *url.URL, seen map[string]bool) []Backend {
	var host string
	if len(route.Spec.Hostnames) > 0 {
		host = string(route.Spec.Hostnames[0])
	}

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

			if host == "" {
				slog.Warn("BYO HTTPRoute has no hostname, skipping",
					"route", route.Name, "namespace", route.Namespace)
				continue
			}

			seen[key] = true
			backends = append(backends, Backend{
				Name:      name,
				Namespace: ns,
				URL:       gwURL,
				Host:      host,
				Ready:     true,
			})
		}
	}
	return backends
}
