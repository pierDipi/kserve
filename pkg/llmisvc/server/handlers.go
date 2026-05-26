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
	"regexp"
)

// DefaultModelIDFilter matches model IDs starting with "publishers/".
var DefaultModelIDFilter = regexp.MustCompile(`^publishers/`)

type ModelsHandlerOption func(*modelsHandlerConfig)

type modelsHandlerConfig struct {
	modelIDFilter *regexp.Regexp
}

// WithModelIDFilter sets a regex pattern to filter model IDs. Only models
// whose ID matches the pattern will be included. Pass nil to disable
// filtering and include all models.
func WithModelIDFilter(pattern *regexp.Regexp) ModelsHandlerOption {
	return func(c *modelsHandlerConfig) {
		c.modelIDFilter = pattern
	}
}

func ModelsHandler(a *Aggregator, opts ...ModelsHandlerOption) http.Handler {
	cfg := &modelsHandlerConfig{
		modelIDFilter: DefaultModelIDFilter,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	mergeFn := MergeModelsWithIDFilter(cfg.modelIDFilter)

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
