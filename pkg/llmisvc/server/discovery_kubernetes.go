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
	"knative.dev/pkg/apis"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KubernetesDiscovery struct {
	client        client.Client
	namespace     string
	labelSelector map[string]string
}

type KubeDiscoveryOption func(*KubernetesDiscovery)

func WithNamespace(ns string) KubeDiscoveryOption {
	return func(d *KubernetesDiscovery) {
		d.namespace = ns
	}
}

func WithLabelSelector(sel map[string]string) KubeDiscoveryOption {
	return func(d *KubernetesDiscovery) {
		d.labelSelector = sel
	}
}

func NewKubernetesDiscovery(c client.Client, opts ...KubeDiscoveryOption) *KubernetesDiscovery {
	d := &KubernetesDiscovery{
		client: c,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

func (d *KubernetesDiscovery) Discover(ctx context.Context) ([]Backend, error) {
	var list v1alpha2.LLMInferenceServiceList

	listOpts := []client.ListOption{}
	if d.namespace != "" {
		listOpts = append(listOpts, client.InNamespace(d.namespace))
	}
	if len(d.labelSelector) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(d.labelSelector))
	}

	if err := d.client.List(ctx, &list, listOpts...); err != nil {
		return nil, fmt.Errorf("listing LLMInferenceService resources: %w", err)
	}

	backends := make([]Backend, 0, len(list.Items))
	for _, item := range list.Items {
		var endpointURL string
		if item.Status.URL != nil {
			endpointURL = item.Status.URL.String()
		} else if len(item.Status.Addresses) > 0 && item.Status.Addresses[0].URL != nil {
			endpointURL = item.Status.Addresses[0].URL.String()
		}

		if endpointURL == "" {
			slog.Warn("skipping backend with no URL", "name", item.Name, "namespace", item.Namespace)
			continue
		}

		parsedURL, err := url.Parse(endpointURL)
		if err != nil {
			slog.Warn("skipping backend with invalid URL", "name", item.Name, "namespace", item.Namespace, "url", endpointURL, "error", err)
			continue
		}

		ready := false
		if cond := item.GetStatus().GetCondition(apis.ConditionReady); cond != nil && cond.IsTrue() {
			ready = true
		}

		backends = append(backends, Backend{
			Name:      item.Name,
			Namespace: item.Namespace,
			URL:       parsedURL,
			Labels:    item.Labels,
			Ready:     ready,
		})
	}

	return backends, nil
}
