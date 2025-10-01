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

package llmisvc_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"knative.dev/pkg/kmeta"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/controller/llmisvc"
	. "github.com/kserve/kserve/pkg/controller/llmisvc/fixture"
)

var _ = Describe("Network Policies", func() {
	Context("Scheduler Network Policy", func() {
		It("should create scheduler network policy when scheduler is configured", func(ctx SpecContext) {
			// given
			svcName := "test-llm-scheduler-netpol"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			// when
			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// then
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
			}).WithContext(ctx).Should(Succeed())

			Expect(expectedNetworkPolicy.Spec.PodSelector.MatchLabels).To(Equal(llmisvc.SchedulerLabels(llmSvc)))
			Expect(expectedNetworkPolicy.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
			Expect(expectedNetworkPolicy.Spec.Ingress).To(HaveLen(1))
			Expect(expectedNetworkPolicy.Spec.Ingress[0].From).ToNot(BeEmpty())

			// Verify gateway namespace selector
			hasGatewaySelector := false
			for _, peer := range expectedNetworkPolicy.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok && ns == "istio-system" {
						hasGatewaySelector = true
						break
					}
				}
			}
			Expect(hasGatewaySelector).To(BeTrue(), "Should have ingress gateway namespace selector")

			// Verify workload namespace selector
			hasWorkloadSelector := false
			for _, peer := range expectedNetworkPolicy.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok && ns == nsName {
						hasWorkloadSelector = true
						break
					}
				}
			}
			Expect(hasWorkloadSelector).To(BeTrue(), "Should have workload namespace selector")
		})

		It("should delete scheduler network policy when scheduler is removed", func(ctx SpecContext) {
			// given
			svcName := "test-llm-scheduler-netpol-delete"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// Verify network policy is created
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
			}).WithContext(ctx).Should(Succeed())

			// when - remove scheduler
			errRetry := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, errUpdate := ctrl.CreateOrUpdate(ctx, envTest.Client, llmSvc, func() error {
					llmSvc.Spec.Router = nil
					return nil
				})
				return errUpdate
			})
			Expect(errRetry).ToNot(HaveOccurred())

			// then - network policy should be deleted
			Eventually(func(g Gomega, ctx context.Context) bool {
				err := envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())
		})

		It("should not create scheduler network policy when network policies are disabled", func(ctx SpecContext) {
			// given
			svcName := "test-llm-scheduler-netpol-disabled"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
				WithNetworkPoliciesDisabled(),
			)

			// when
			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// then - network policy should not be created
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Consistently(func(g Gomega, ctx context.Context) bool {
				err := envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())
		})
	})

	Context("Workload Network Policy", func() {
		It("should create workload network policy when scheduler is configured", func(ctx SpecContext) {
			// given
			svcName := "test-llm-workload-netpol"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			// when
			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// then
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-workload"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
			}).WithContext(ctx).Should(Succeed())

			Expect(expectedNetworkPolicy.Spec.PodSelector.MatchLabels).To(Equal(llmisvc.GetWorkloadLabelSelector(llmSvc.ObjectMeta, &llmSvc.Spec)))
			Expect(expectedNetworkPolicy.Spec.PolicyTypes).To(ContainElement(networkingv1.PolicyTypeIngress))
			Expect(expectedNetworkPolicy.Spec.Ingress).To(HaveLen(1))
			Expect(expectedNetworkPolicy.Spec.Ingress[0].From).ToNot(BeEmpty())

			// Verify gateway namespace selector
			hasGatewaySelector := false
			for _, peer := range expectedNetworkPolicy.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok && ns == "istio-system" {
						hasGatewaySelector = true
						break
					}
				}
			}
			Expect(hasGatewaySelector).To(BeTrue(), "Should have ingress gateway namespace selector")

			// Verify workload namespace selector
			hasWorkloadSelector := false
			for _, peer := range expectedNetworkPolicy.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok && ns == nsName {
						hasWorkloadSelector = true
						break
					}
				}
			}
			Expect(hasWorkloadSelector).To(BeTrue(), "Should have workload namespace selector")
		})

		It("should delete workload network policy when scheduler is removed", func(ctx SpecContext) {
			// given
			svcName := "test-llm-workload-netpol-delete"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// Verify network policy is created
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-workload"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
			}).WithContext(ctx).Should(Succeed())

			// when - remove scheduler
			errRetry := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				_, errUpdate := ctrl.CreateOrUpdate(ctx, envTest.Client, llmSvc, func() error {
					llmSvc.Spec.Router = nil
					return nil
				})
				return errUpdate
			})
			Expect(errRetry).ToNot(HaveOccurred())

			// then - network policy should be deleted
			Eventually(func(g Gomega, ctx context.Context) bool {
				err := envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-workload"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())
		})

		It("should not create workload network policy when network policies are disabled", func(ctx SpecContext) {
			// given
			svcName := "test-llm-workload-netpol-disabled"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
				WithNetworkPoliciesDisabled(),
			)

			// when
			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// then - network policy should not be created
			expectedNetworkPolicy := &networkingv1.NetworkPolicy{}
			Consistently(func(g Gomega, ctx context.Context) bool {
				err := envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-workload"),
					Namespace: nsName,
				}, expectedNetworkPolicy)
				return errors.IsNotFound(err)
			}).WithContext(ctx).Should(BeTrue())
		})
	})

	Context("Monitoring Namespaces", func() {
		It("should allow traffic from monitoring namespaces", func(ctx SpecContext) {
			// given
			svcName := "test-llm-monitoring-namespaces"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			// when
			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// then - verify scheduler network policy has monitoring namespace selectors
			schedulerNetPol := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, schedulerNetPol)
			}).WithContext(ctx).Should(Succeed())

			hasMonitoringSelector := false
			for _, peer := range schedulerNetPol.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok {
						if ns == "openshift-monitoring" || ns == "openshift-user-workload-monitoring" {
							hasMonitoringSelector = true
							break
						}
					}
				}
			}
			Expect(hasMonitoringSelector).To(BeTrue(), "Should have monitoring namespace selector")

			// then - verify workload network policy has monitoring namespace selectors
			workloadNetPol := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-workload"),
					Namespace: nsName,
				}, workloadNetPol)
			}).WithContext(ctx).Should(Succeed())

			hasMonitoringSelector = false
			for _, peer := range workloadNetPol.Spec.Ingress[0].From {
				if peer.NamespaceSelector != nil {
					if ns, ok := peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"]; ok {
						if ns == "openshift-monitoring" || ns == "openshift-user-workload-monitoring" {
							hasMonitoringSelector = true
							break
						}
					}
				}
			}
			Expect(hasMonitoringSelector).To(BeTrue(), "Should have monitoring namespace selector")
		})
	})

	Context("Network Policy Updates", func() {
		It("should update network policies when labels change", func(ctx SpecContext) {
			// given
			svcName := "test-llm-netpol-update"
			nsName := kmeta.ChildName(svcName, "-test")
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						"kubernetes.io/metadata.name": nsName,
					},
				},
			}

			Expect(envTest.Client.Create(ctx, namespace)).To(Succeed())
			Expect(envTest.Client.Create(ctx, IstioShadowService(svcName, nsName))).To(Succeed())
			defer func() {
				envTest.DeleteAll(namespace)
			}()

			llmSvc := LLMInferenceService(svcName,
				InNamespace[*v1alpha1.LLMInferenceService](nsName),
				WithModelURI("hf://facebook/opt-125m"),
				WithManagedScheduler(),
			)

			Expect(envTest.Create(ctx, llmSvc)).To(Succeed())
			defer func() {
				Expect(envTest.Delete(ctx, llmSvc)).To(Succeed())
			}()

			// Verify initial network policies are created
			schedulerNetPol := &networkingv1.NetworkPolicy{}
			Eventually(func(g Gomega, ctx context.Context) error {
				return envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, schedulerNetPol)
			}).WithContext(ctx).Should(Succeed())

			originalLabels := schedulerNetPol.Labels

			// when - update the llmisvc (trigger reconciliation)
			errRetry := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				current := &v1alpha1.LLMInferenceService{}
				if err := envTest.Get(ctx, client.ObjectKeyFromObject(llmSvc), current); err != nil {
					return err
				}
				if current.Annotations == nil {
					current.Annotations = make(map[string]string)
				}
				current.Annotations["test-annotation"] = "test-value"
				return envTest.Update(ctx, current)
			})
			Expect(errRetry).ToNot(HaveOccurred())

			// then - verify network policy still exists with correct labels
			Eventually(func(g Gomega, ctx context.Context) error {
				updated := &networkingv1.NetworkPolicy{}
				if err := envTest.Get(ctx, types.NamespacedName{
					Name:      kmeta.ChildName(svcName, "-kserve-router-scheduler"),
					Namespace: nsName,
				}, updated); err != nil {
					return err
				}
				g.Expect(updated.Labels).To(Equal(originalLabels))
				return nil
			}).WithContext(ctx).Should(Succeed())
		})
	})
})
