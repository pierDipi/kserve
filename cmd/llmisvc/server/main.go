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

package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	kservescheme "github.com/kserve/kserve/pkg/scheme"

	aggserver "github.com/kserve/kserve/pkg/llmisvc/server"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kservescheme.AddLLMISVCAPIs(scheme))
}

func main() {
	var (
		listenAddr    string
		backendTimeout time.Duration
		failurePolicy string
		namespace     string
		kubeconfig    string
	)

	flag.StringVar(&listenAddr, "listen-addr", ":8080", "Address to listen on")
	flag.DurationVar(&backendTimeout, "backend-timeout", 10*time.Second, "Timeout for backend requests")
	flag.StringVar(&failurePolicy, "failure-policy", "return-partial", "Partial failure policy: fail-all or return-partial")
	flag.StringVar(&namespace, "namespace", "", "Limit discovery to a specific namespace")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	flag.Parse()

	if kubeconfig != "" {
		if err := os.Setenv("KUBECONFIG", kubeconfig); err != nil {
			slog.Error("failed to set KUBECONFIG", "error", err)
			os.Exit(1)
		}
	}

	cfg, err := config.GetConfig()
	if err != nil {
		slog.Error("unable to get kubeconfig", "error", err)
		os.Exit(1)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		slog.Error("unable to create kubernetes client", "error", err)
		os.Exit(1)
	}

	var discoveryOpts []aggserver.KubeDiscoveryOption
	if namespace != "" {
		discoveryOpts = append(discoveryOpts, aggserver.WithNamespace(namespace))
	}
	discovery := aggserver.NewKubernetesDiscovery(k8sClient, discoveryOpts...)

	policy := aggserver.ReturnPartial
	if failurePolicy == "fail-all" {
		policy = aggserver.FailAll
	}

	aggregator := aggserver.NewAggregator(discovery,
		aggserver.WithTimeout(backendTimeout),
		aggserver.WithFailurePolicy(policy),
	)

	mux := http.NewServeMux()
	mux.Handle("/v1/models", aggserver.ModelsHandler(aggregator))
	mux.Handle("/health", aggserver.HealthHandler(aggregator))
	mux.Handle("/metrics", aggserver.MetricsHandler(aggregator))
	mux.Handle("/load", aggserver.LoadHandler(aggregator))
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("starting aggregation server", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}
