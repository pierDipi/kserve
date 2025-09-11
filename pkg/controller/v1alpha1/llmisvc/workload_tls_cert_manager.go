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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/network"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	igwapi "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	certmanagerapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagermetaapi "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
)

const (
	certManagerSecretSuffix = "-kserve-tls-certs" //nolint:gosec
)

// reconcileCertManagerCertificate reconciles the cert-manager Certificate used by the server to serve TLS.
func (r *LLMISVCReconciler) reconcileCertManagerCertificate(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService, config *Config) error {
	log.FromContext(ctx).Info("Reconciling cert-manage certificate")

	certificate, err := r.expectedCertManagerCertificate(ctx, llmSvc, config)
	if err != nil {
		return fmt.Errorf("failed to get expected cert manager certificate: %w", err)
	}

	if !config.CertManagerConfig.IsEnabled() {
		return Delete(ctx, r, llmSvc, certificate)
	}

	return Reconcile(ctx, r, llmSvc, &certmanagerapi.Certificate{}, certificate, semanticCertificateIsEqual)
}

func (r *LLMISVCReconciler) expectedCertManagerCertificate(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService, config *Config) (*certmanagerapi.Certificate, error) {
	ipAddresses, err := r.collectIPAddresses(ctx, llmSvc)
	if err != nil {
		return nil, fmt.Errorf("failed to collect IP addresses: %w", err)
	}

	c := &certmanagerapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: llmSvc.GetNamespace(),
			Name:      kmeta.ChildName(llmSvc.GetName(), "-kserve-certificate"),
			Labels: map[string]string{
				"app.kubernetes.io/component": "llminferenceservice-workload",
				"app.kubernetes.io/name":      llmSvc.GetName(),
				"app.kubernetes.io/part-of":   "llminferenceservice",
			},
		},
		Spec: certmanagerapi.CertificateSpec{
			Subject: &certmanagerapi.X509Subject{
				Organizations: []string{"local"},
			},
			Duration:              ptr.To(config.CertManagerConfig.Duration),
			RenewBeforePercentage: ptr.To(int32(20)),
			DNSNames:              r.collectDNSNames(ctx, llmSvc),
			IPAddresses:           ipAddresses,
			SecretName:            kmeta.ChildName(llmSvc.GetName(), certManagerSecretSuffix),
			SecretTemplate: &certmanagerapi.CertificateSecretTemplate{
				Labels: map[string]string{
					"app.kubernetes.io/name":    llmSvc.GetName(),
					"app.kubernetes.io/part-of": "llminferenceservice",
				},
			},
			IssuerRef: certmanagermetaapi.ObjectReference{
				Name:  config.CertManagerConfig.IssuerName,
				Kind:  config.CertManagerConfig.IssuerKind,
				Group: config.CertManagerConfig.IssuerGroup,
			},
			IsCA: false,
			Usages: []certmanagerapi.KeyUsage{
				certmanagerapi.UsageServerAuth,
				certmanagerapi.UsageKeyEncipherment,
				certmanagerapi.UsageDigitalSignature,
			},
			PrivateKey: &certmanagerapi.CertificatePrivateKey{
				RotationPolicy: certmanagerapi.RotationPolicyAlways,
				Encoding:       certmanagerapi.PKCS8,
				Algorithm:      certmanagerapi.Ed25519KeyAlgorithm,
			},
		},
	}

	log.FromContext(ctx).V(2).Info("Expected cert-manager certificate", "certificate", c)

	return c, nil
}

func (r *LLMISVCReconciler) collectDNSNames(ctx context.Context, llmSvc *v1alpha1.LLMInferenceService) []string {
	dnsNames := []string{
		network.GetServiceHostname(kmeta.ChildName(llmSvc.GetName(), "-kserve-workload-svc"), llmSvc.GetNamespace()),
		fmt.Sprintf("%s.%s.svc", kmeta.ChildName(llmSvc.GetName(), "-kserve-workload-svc"), llmSvc.GetNamespace()),
	}

	if llmSvc.Spec.Router != nil && llmSvc.Spec.Router.Scheduler != nil && llmSvc.Spec.Router.Scheduler.Pool != nil {
		ipSpec := llmSvc.Spec.Router.Scheduler.Pool.Spec
		if llmSvc.Spec.Router.Scheduler.Pool.HasRef() {
			ip := &igwapi.InferencePool{
				ObjectMeta: metav1.ObjectMeta{Namespace: llmSvc.GetNamespace(), Name: llmSvc.Spec.Router.Scheduler.Pool.Ref.Name},
			}

			// If there is an error, this will be reported properly as part of the Router reconciliation.
			if err := r.Client.Get(ctx, client.ObjectKeyFromObject(ip), ip); err == nil {
				ipSpec = &ip.Spec
			}
		}

		if ipSpec != nil {
			dnsNames = append(dnsNames, network.GetServiceHostname(string(ipSpec.ExtensionRef.Name), llmSvc.GetNamespace()))
			dnsNames = append(dnsNames, fmt.Sprintf("%s.%s.svc", string(ipSpec.ExtensionRef.Name), llmSvc.GetNamespace()))
		}
	}

	return dnsNames
}

func (r *LLMISVCReconciler) collectIPAddresses(ctx context.Context, svc *v1alpha1.LLMInferenceService) ([]string, error) {
	pods := &corev1.PodList{}
	listOptions := &client.ListOptions{
		Namespace: svc.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"app.kubernetes.io/name":    svc.Name,
			"app.kubernetes.io/part-of": "llminferenceservice",
		}),
	}

	if err := r.Client.List(ctx, pods, listOptions); err != nil {
		return nil, fmt.Errorf("failed to list pods associated with LLM inference service: %w", err)
	}

	ips := sets.NewString()
	for _, pod := range pods.Items {
		ips.Insert(pod.Status.PodIP)
		for _, ip := range pod.Status.PodIPs {
			ips.Insert(ip.String())
		}
	}

	// List sorts IPs, so that the resulting list is always the same regardless of the order of the IPs.
	return ips.List(), nil
}

func semanticCertificateIsEqual(expected *certmanagerapi.Certificate, curr *certmanagerapi.Certificate) bool {
	return equality.Semantic.DeepEqual(expected.Spec, curr.Spec) &&
		equality.Semantic.DeepDerivative(expected.Labels, curr.Labels) &&
		equality.Semantic.DeepDerivative(expected.Annotations, curr.Annotations)
}
