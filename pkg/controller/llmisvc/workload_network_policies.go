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

package llmisvc

import (
	"context"
	"fmt"
	"strings"

	"github.com/kserve/kserve/pkg/constants"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/kmeta"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

// +kubebuilder:rbac:groups="networking.k8s.io",resources=networkpolicies,verbs=get;list;watch;create;update;delete

const (
	networkPoliciesNamespaceNameKey = "kubernetes.io/metadata.name"
)

var (
	ocpMonitoringNamespaces = constants.GetEnvOrDefault("OCP_MONITORING_NAMESPACES", "openshift-monitoring,openshift-user-workload-monitoring")
)

func (r *LLMInferenceServiceReconciler) reconcileNetworkPolicies(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService) error {
	if err := r.reconcileSchedulerNetworkPolicy(ctx, llmSvc); err != nil {
		return fmt.Errorf("failed to reconcile scheduler network policy: %w", err)
	}
	if err := r.reconcileWorkloadNetworkPolicy(ctx, llmSvc); err != nil {
		return fmt.Errorf("failed to reconcile workload network policy: %w", err)
	}
	return nil
}

func (r *LLMInferenceServiceReconciler) reconcileSchedulerNetworkPolicy(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService) error {
	cfg, err := LoadConfig(ctx, r.Clientset)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	expected := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kmeta.ChildName(llmSvc.GetName(), "-kserve-router-scheduler"),
			Namespace: llmSvc.GetNamespace(),
			Labels:    SchedulerLabels(llmSvc),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(llmSvc, v1alpha1.SchemeGroupVersion.WithKind("LLMInferenceService")),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Only restrict ingress traffic since scheduler might need to download models (for kv-cache aware routing)
			// from arbitrary locations.
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			PodSelector: metav1.LabelSelector{
				MatchLabels: SchedulerLabels(llmSvc),
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						// Gateway Traffic
						{NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{networkPoliciesNamespaceNameKey: cfg.IngressGatewayNamespace},
						}},
						// Scheduler and Inference traffic (NIXL, etc)
						{NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{networkPoliciesNamespaceNameKey: llmSvc.GetNamespace()},
						}},
					},
				},
			},
		},
	}

	// Monitoring (scraping)
	allowMonitoringNamespaces(expected)

	if llmSvc.IsNetworkPoliciesDisabled() || llmSvc.Spec.Router == nil || llmSvc.Spec.Router.Scheduler == nil {
		return Delete(ctx, r, llmSvc, expected)
	}
	return Reconcile(ctx, r, llmSvc, &networkingv1.NetworkPolicy{}, expected, semanticNetworkPolicyIsEqual)
}

func (r *LLMInferenceServiceReconciler) reconcileWorkloadNetworkPolicy(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService) error {
	cfg, err := LoadConfig(ctx, r.Clientset)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	expected := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kmeta.ChildName(llmSvc.GetName(), "-kserve-workload"),
			Namespace: llmSvc.GetNamespace(),
			Labels:    SchedulerLabels(llmSvc),
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(llmSvc, v1alpha1.SchemeGroupVersion.WithKind("LLMInferenceService")),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			// Only restrict ingress traffic since runtime need to download models from arbitrary locations.
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			PodSelector: metav1.LabelSelector{
				MatchLabels: GetWorkloadLabelSelector(llmSvc.ObjectMeta, &llmSvc.Spec),
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						// Gateway Traffic
						{NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{networkPoliciesNamespaceNameKey: cfg.IngressGatewayNamespace},
						}},
						// Scheduler and Inference traffic (NIXL, etc)
						{NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{networkPoliciesNamespaceNameKey: llmSvc.GetNamespace()},
						}},
					},
				},
			},
		},
	}

	// Monitoring (scraping)
	allowMonitoringNamespaces(expected)

	if llmSvc.IsNetworkPoliciesDisabled() || llmSvc.Spec.Router == nil || llmSvc.Spec.Router.Scheduler == nil {
		return Delete(ctx, r, llmSvc, expected)
	}
	return Reconcile(ctx, r, llmSvc, &networkingv1.NetworkPolicy{}, expected, semanticNetworkPolicyIsEqual)
}

func allowMonitoringNamespaces(expected *networkingv1.NetworkPolicy) {
	for _, ns := range monitoringNamespaces() {
		for i := range expected.Spec.Ingress {
			expected.Spec.Ingress[i].From = append(expected.Spec.Ingress[0].From, networkingv1.NetworkPolicyPeer{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{networkPoliciesNamespaceNameKey: ns},
				},
			})
		}
	}
}

func monitoringNamespaces() []string {
	namespaces := strings.Split(ocpMonitoringNamespaces, ",")
	for i := range namespaces {
		namespaces[i] = strings.TrimSpace(namespaces[i])
	}
	return namespaces
}

func semanticNetworkPolicyIsEqual(expected *networkingv1.NetworkPolicy, curr *networkingv1.NetworkPolicy) bool {
	return equality.Semantic.DeepDerivative(expected.Spec, curr.Spec) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations)
}
