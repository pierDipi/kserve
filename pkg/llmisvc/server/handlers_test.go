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
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestModelsHandlerReturnsValidResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"meta-llama/llama-3","object":"model","created":100,"owned_by":"meta"},
			{"id":"publishers/default/models/meta-llama/llama-3","object":"model","created":100,"owned_by":"meta"}
		]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "llama-backend", Namespace: "default", URL: u, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := ModelsHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp ListModelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Object != "list" {
		t.Fatalf("expected object 'list', got %s", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model (default prefix filter), got %d", len(resp.Data))
	}

	var m map[string]any
	if err := json.Unmarshal(resp.Data[0], &m); err != nil {
		t.Fatalf("failed to unmarshal model: %v", err)
	}
	if m["id"] != "publishers/default/models/meta-llama/llama-3" {
		t.Fatalf("expected publishers/ model ID, got %v", m["id"])
	}
	if m["owned_by"] != "llama-backend/default" {
		t.Fatalf("expected owned_by 'llama-backend/default', got %v", m["owned_by"])
	}
}

func TestModelsHandlerDefaultPrefixFiltering(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"facebook/opt-125m","object":"model","created":1779787746,"owned_by":"vllm"},
			{"id":"publishers/default/models/facebook/opt-125m","object":"model","created":1779787746,"owned_by":"vllm"}
		]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "opt-svc", Namespace: "default", URL: u, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := ModelsHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp ListModelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model with default publishers/ filter, got %d", len(resp.Data))
	}

	var m map[string]any
	if err := json.Unmarshal(resp.Data[0], &m); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if m["id"] != "publishers/default/models/facebook/opt-125m" {
		t.Fatalf("expected publishers/ model, got %v", m["id"])
	}
}

func TestModelsHandlerNoFilterIncludesAll(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"facebook/opt-125m","object":"model"},
			{"id":"publishers/default/models/facebook/opt-125m","object":"model"}
		]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "svc", Namespace: "ns", URL: u, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := ModelsHandler(agg, WithModelIDFilter(nil))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var resp ListModelsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 models with nil filter, got %d", len(resp.Data))
	}
}

func TestHealthHandlerAllHealthy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy"}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "b1", Namespace: "ns", URL: u, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := HealthHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var hs HealthStatus
	if err := json.NewDecoder(rec.Body).Decode(&hs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if hs.Status != "healthy" {
		t.Fatalf("expected healthy status, got %s", hs.Status)
	}
}

func TestHealthHandlerUnhealthyBackend(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthy.Close()

	u1, _ := url.Parse(healthy.URL)
	u2, _ := url.Parse(unhealthy.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "healthy", Namespace: "ns", URL: u1, Ready: true},
		{Name: "unhealthy", Namespace: "ns", URL: u2, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := HealthHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}

	var hs HealthStatus
	if err := json.NewDecoder(rec.Body).Decode(&hs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if hs.Status != "unhealthy" {
		t.Fatalf("expected unhealthy status, got %s", hs.Status)
	}
}

func TestHealthHandlerEmptyBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "vllm-backend", Namespace: "default", URL: u, Ready: true},
	})
	agg := NewAggregator(discovery)

	handler := HealthHandler(agg)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var hs HealthStatus
	if err := json.NewDecoder(rec.Body).Decode(&hs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if hs.Status != "healthy" {
		t.Fatalf("expected healthy, got %s", hs.Status)
	}
}

func TestHandlersMethodNotAllowed(t *testing.T) {
	discovery := NewStaticDiscovery([]Backend{})
	agg := NewAggregator(discovery)

	handlers := map[string]http.Handler{
		"/v1/models": ModelsHandler(agg),
		"/health":    HealthHandler(agg),
		"/metrics":   MetricsHandler(agg),
		"/load":      LoadHandler(agg),
	}

	for path, handler := range handlers {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("handler %s: expected status 405 for POST, got %d", path, rec.Code)
		}
	}
}
