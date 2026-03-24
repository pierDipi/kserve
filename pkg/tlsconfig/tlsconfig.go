//go:build distro

/*
Copyright 2026 The KServe Authors.

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

// Package tlsconfig provides helpers for fetching the cluster-wide TLS security
// profile from the OpenShift APIServer resource and applying it to Go TLS
// configurations.
//
// On non-OpenShift clusters (e.g. xKS), the APIServer CRD does not exist.
// All functions in this package handle that case gracefully by returning nil
// (no TLS overrides), allowing the caller to proceed with default TLS settings.
package tlsconfig

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var apiserverGVR = schema.GroupVersionResource{
	Group:    "config.openshift.io",
	Version:  "v1",
	Resource: "apiservers",
}

// FetchTLSOpts fetches the cluster-wide TLS security profile from the
// APIServer resource and returns a slice of tls.Config modifier functions
// suitable for use with controller-runtime's TLSOpts.
//
// On non-OpenShift clusters (xKS) where the APIServer CRD does not exist,
// this returns (nil, nil) so the caller can proceed with default TLS settings.
func FetchTLSOpts(ctx context.Context, cl client.Client) ([]func(*tls.Config), error) {
	profile, err := fetchTLSProfile(ctx, cl)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, nil
	}

	spec, err := resolveProfileSpec(profile)
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx)
	logger.Info("Applying cluster TLS security profile",
		"type", profile.Type,
		"minTLSVersion", spec.MinTLSVersion)

	return []func(*tls.Config){newTLSConfigFn(spec)}, nil
}

// FetchTLSConfig fetches the cluster-wide TLS security profile and returns
// a *tls.Config configured with the profile's MinVersion and CipherSuites.
//
// On non-OpenShift clusters (xKS) where the APIServer CRD does not exist,
// this returns (nil, nil) so the caller can proceed with default TLS settings.
func FetchTLSConfig(ctx context.Context, cl client.Client) (*tls.Config, error) {
	profile, err := fetchTLSProfile(ctx, cl)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, nil
	}

	spec, err := resolveProfileSpec(profile)
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx)
	logger.Info("Applying cluster TLS security profile",
		"type", profile.Type,
		"minTLSVersion", spec.MinTLSVersion)

	cfg := &tls.Config{} //nolint:gosec
	newTLSConfigFn(spec)(cfg)
	return cfg, nil
}

// WatchAndExitOnTLSProfileChange watches the APIServer resource for TLS
// profile changes and calls os.Exit(0) when a change is detected, allowing
// the pod to restart with the new profile.
//
// On non-OpenShift clusters (xKS) where the APIServer CRD does not exist,
// this function returns immediately (no-op).
//
// This function blocks and should be called in a goroutine.
func WatchAndExitOnTLSProfileChange(ctx context.Context, restConfig *rest.Config) {
	logger := log.FromContext(ctx).WithName("tls-profile-watcher")

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		logger.Error(err, "Failed to create dynamic client for TLS profile watcher")
		return
	}

	// Fetch the current profile to compare against.
	// On xKS clusters the CRD does not exist, so we get a NotFound error.
	initial, err := dynClient.Resource(apiserverGVR).Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		if isNotFoundOrCRDMissing(err) {
			logger.Info("APIServer CRD not found (non-OpenShift cluster), skipping TLS profile watch")
			return
		}
		logger.Error(err, "Failed to get initial APIServer config for TLS profile watcher")
		return
	}
	initialAPIServer := &configv1.APIServer{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(initial.Object, initialAPIServer); err != nil {
		logger.Error(err, "Failed to convert initial APIServer object")
		return
	}
	currentProfile := initialAPIServer.Spec.TLSSecurityProfile

	w, err := dynClient.Resource(apiserverGVR).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=cluster",
	})
	if err != nil {
		logger.Error(err, "Failed to start TLS profile watch")
		return
	}
	defer w.Stop()

	for event := range w.ResultChan() {
		if event.Type != watch.Modified {
			continue
		}
		unstructuredObj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			continue
		}

		apiServer := &configv1.APIServer{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, apiServer); err != nil {
			logger.Error(err, "Failed to convert unstructured APIServer object")
			continue
		}

		if !equality.Semantic.DeepEqual(currentProfile, apiServer.Spec.TLSSecurityProfile) {
			logger.Info("Cluster TLS security profile changed, exiting to apply new profile")
			os.Exit(0)
		}
	}
}

// fetchTLSProfile fetches the TLS profile from the APIServer resource.
// Returns (nil, nil) when the APIServer CRD does not exist (non-OpenShift cluster).
func fetchTLSProfile(ctx context.Context, cl client.Client) (*configv1.TLSSecurityProfile, error) {
	apiServer := &configv1.APIServer{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "cluster"}, apiServer); err != nil {
		if isNotFoundOrCRDMissing(err) {
			log.FromContext(ctx).Info("APIServer CRD not found (non-OpenShift cluster), skipping TLS profile")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get APIServer cluster config: %w", err)
	}

	profile := apiServer.Spec.TLSSecurityProfile
	if profile == nil {
		profile = &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileIntermediateType,
		}
	}
	return profile, nil
}

// isNotFoundOrCRDMissing returns true when the error indicates that either
// the specific resource or its CRD does not exist on the cluster.
func isNotFoundOrCRDMissing(err error) bool {
	if apierrors.IsNotFound(err) {
		return true
	}
	// When the CRD itself is not installed, the API server returns a
	// "no matches for kind" error which surfaces as a NoMatch discovery error.
	// The dynamic client may also return a 404 with "the server could not find
	// the requested resource" for missing CRDs.
	if statusErr, ok := err.(*apierrors.StatusError); ok {
		code := statusErr.Status().Code
		return code == 404
	}
	return false
}

func resolveProfileSpec(profile *configv1.TLSSecurityProfile) (*configv1.TLSProfileSpec, error) {
	switch profile.Type {
	case configv1.TLSProfileOldType,
		configv1.TLSProfileIntermediateType,
		configv1.TLSProfileModernType:
		spec, ok := configv1.TLSProfiles[profile.Type]
		if !ok {
			return configv1.TLSProfiles[configv1.TLSProfileIntermediateType], nil
		}
		return spec, nil
	case configv1.TLSProfileCustomType:
		if profile.Custom == nil {
			return nil, fmt.Errorf("custom TLS profile specified but Custom is nil")
		}
		return &profile.Custom.TLSProfileSpec, nil
	default:
		return configv1.TLSProfiles[configv1.TLSProfileIntermediateType], nil
	}
}

func newTLSConfigFn(spec *configv1.TLSProfileSpec) func(*tls.Config) {
	return func(cfg *tls.Config) {
		minVer, err := parseTLSVersion(string(spec.MinTLSVersion))
		if err != nil {
			// Fall back to TLS 1.2 if version is unrecognized
			minVer = tls.VersionTLS12
		}
		cfg.MinVersion = minVer

		// TLS 1.3 cipher suites are not configurable in Go (golang/go#29349)
		if minVer < tls.VersionTLS13 {
			suites := mapCipherSuites(spec.Ciphers)
			if len(suites) > 0 {
				cfg.CipherSuites = suites
			}
		}
	}
}

func parseTLSVersion(v string) (uint16, error) {
	versions := map[string]uint16{
		"VersionTLS10": tls.VersionTLS10,
		"VersionTLS11": tls.VersionTLS11,
		"VersionTLS12": tls.VersionTLS12,
		"VersionTLS13": tls.VersionTLS13,
	}
	if ver, ok := versions[v]; ok {
		return ver, nil
	}
	return 0, fmt.Errorf("unknown TLS version: %s", v)
}

// mapCipherSuites converts OpenSSL-style cipher names (used in OpenShift TLS
// profiles) to Go crypto/tls constants. Ciphers without a Go constant are
// silently skipped.
func mapCipherSuites(names []string) []uint16 {
	m := map[string]uint16{
		"ECDHE-RSA-AES128-GCM-SHA256":   tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		"ECDHE-ECDSA-AES128-GCM-SHA256": tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		"ECDHE-RSA-AES256-GCM-SHA384":   tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		"ECDHE-ECDSA-AES256-GCM-SHA384": tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		"ECDHE-RSA-CHACHA20-POLY1305":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		"ECDHE-ECDSA-CHACHA20-POLY1305": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		"ECDHE-RSA-AES128-SHA256":       tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
		"ECDHE-ECDSA-AES128-SHA256":     tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
		"ECDHE-RSA-AES128-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		"ECDHE-ECDSA-AES128-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		"ECDHE-RSA-AES256-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		"ECDHE-ECDSA-AES256-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		"AES128-GCM-SHA256":             tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		"AES256-GCM-SHA384":             tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		"AES128-SHA256":                 tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
		"AES128-SHA":                    tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		"AES256-SHA":                    tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		"DES-CBC3-SHA":                  tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	}

	out := make([]uint16, 0, len(names))
	for _, name := range names {
		if id, ok := m[name]; ok {
			out = append(out, id)
		}
	}
	return out
}
