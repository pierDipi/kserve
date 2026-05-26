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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// DefaultModelIDPrefixes is the default set of model ID prefixes to include
// in aggregated /v1/models responses. Models whose ID does not start with
// one of these prefixes are filtered out.
var DefaultModelIDPrefixes = []string{"publishers/"}

type ModelsHandlerOption func(*modelsHandlerConfig)

type modelsHandlerConfig struct {
	modelIDPrefixes []string
}

// WithModelIDPrefixes sets the model ID prefixes to filter by. Only models
// whose ID starts with one of the given prefixes will be included. Pass an
// empty slice to disable filtering and include all models.
func WithModelIDPrefixes(prefixes ...string) ModelsHandlerOption {
	return func(c *modelsHandlerConfig) {
		c.modelIDPrefixes = prefixes
	}
}

func ModelsHandler(a *Aggregator, opts ...ModelsHandlerOption) http.Handler {
	cfg := &modelsHandlerConfig{
		modelIDPrefixes: DefaultModelIDPrefixes,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	mergeFn := MergeModelsWithPrefixes(cfg.modelIDPrefixes...)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		result, status, err := a.FanOut(r.Context(), r, "/v1/models", mergeFn)
		if err != nil {
			slog.Error("fan-out failed for /v1/models", "error", err)
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, status, result)
	})
}

func HealthHandler(a *Aggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		result, status, err := a.FanOut(r.Context(), r, "/health", MergeHealthResponses)
		if err != nil {
			slog.Error("fan-out failed for /health", "error", err)
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, status, result)
	})
}

func MetricsHandler(a *Aggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		result, status, err := a.FanOut(r.Context(), r, "/metrics", MergeMetricsResponses)
		if err != nil {
			slog.Error("fan-out failed for /metrics", "error", err)
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		fmt.Fprint(w, result)
	})
}

func LoadHandler(a *Aggregator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		result, status, err := a.FanOut(r.Context(), r, "/load", MergeLoadResponses)
		if err != nil {
			slog.Error("fan-out failed for /load", "error", err)
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, status, result)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
