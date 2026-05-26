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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestFanOutMultipleBackends(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"model-a","object":"model","created":1,"owned_by":"test"}]}`))
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"model-b","object":"model","created":2,"owned_by":"test"}]}`))
	}))
	defer srv2.Close()

	u1, _ := url.Parse(srv1.URL)
	u2, _ := url.Parse(srv2.URL)

	discovery := NewStaticDiscovery([]Backend{
		{Name: "backend-1", Namespace: "ns-1", URL: u1, Ready: true},
		{Name: "backend-2", Namespace: "ns-2", URL: u2, Ready: true},
	})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	result, status, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	resp, ok := result.(ListModelsResponse)
	if !ok {
		t.Fatalf("expected ListModelsResponse, got %T", result)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Data))
	}
}

func TestFanOutTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "slow-backend", Namespace: "ns", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery, WithTimeout(100*time.Millisecond))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	result, status, err := agg.FanOut(req.Context(), req, "/health", MergeHealthResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hs, ok := result.(HealthStatus)
	if !ok {
		t.Fatalf("expected HealthStatus, got %T", result)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", status)
	}
	if len(hs.Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(hs.Backends))
	}
	if hs.Backends[0].Status != "unreachable" {
		t.Fatalf("expected unreachable status, got %s", hs.Backends[0].Status)
	}
}

func TestFanOutFailAllPolicy(t *testing.T) {
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer srvOK.Close()

	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail.Close()

	u1, _ := url.Parse(srvOK.URL)
	u2, _ := url.Parse(srvFail.URL)

	discovery := NewStaticDiscovery([]Backend{
		{Name: "ok", Namespace: "ns", URL: u1, Ready: true},
		{Name: "fail", Namespace: "ns", URL: u2, Ready: true},
	})

	agg := NewAggregator(discovery, WithFailurePolicy(FailAll))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	_, status, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err == nil {
		t.Fatal("expected error with FailAll policy when a backend fails")
	}
	if status != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", status)
	}
}

func TestFanOutReturnPartialPolicy(t *testing.T) {
	srvOK2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"model-ok","object":"model"}]}`))
	}))
	defer srvOK2.Close()

	srvFail2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvFail2.Close()

	u1, _ := url.Parse(srvOK2.URL)
	u2, _ := url.Parse(srvFail2.URL)

	discovery := NewStaticDiscovery([]Backend{
		{Name: "ok", Namespace: "ns", URL: u1, Ready: true},
		{Name: "fail", Namespace: "ns", URL: u2, Ready: true},
	})

	agg := NewAggregator(discovery, WithFailurePolicy(ReturnPartial))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	result, status, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	resp, ok := result.(ListModelsResponse)
	if !ok {
		t.Fatalf("expected ListModelsResponse, got %T", result)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Data))
	}
}

func TestFanOutFilterFunction(t *testing.T) {
	called := make(map[string]bool)

	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called[name] = true
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(fmt.Sprintf(`{"object":"list","data":[{"id":"model-%s","object":"model"}]}`, name)))
		})
	}

	srv1 := httptest.NewServer(handler("include"))
	defer srv1.Close()
	srv2 := httptest.NewServer(handler("exclude"))
	defer srv2.Close()

	u1, _ := url.Parse(srv1.URL)
	u2, _ := url.Parse(srv2.URL)

	discovery := NewStaticDiscovery([]Backend{
		{Name: "include", Namespace: "ns", URL: u1, Ready: true, Labels: map[string]string{"tier": "prod"}},
		{Name: "exclude", Namespace: "ns", URL: u2, Ready: true, Labels: map[string]string{"tier": "staging"}},
	})

	filter := func(r *http.Request, b Backend) bool {
		return b.Labels["tier"] == "prod"
	}

	agg := NewAggregator(discovery, WithFilter(filter))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	result, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp := result.(ListModelsResponse)
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Data))
	}
	if !called["include"] {
		t.Fatal("expected include backend to be called")
	}
	if called["exclude"] {
		t.Fatal("expected exclude backend not to be called")
	}
}

func TestFanOutZeroBackends(t *testing.T) {
	discovery := NewStaticDiscovery([]Backend{})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	result, status, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	resp, ok := result.(ListModelsResponse)
	if !ok {
		t.Fatalf("expected ListModelsResponse, got %T", result)
	}
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 models, got %d", len(resp.Data))
	}
}

func TestForwardHeadersDefault(t *testing.T) {
	var receivedAuth string
	var receivedCookie string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCookie = r.Header.Get("Cookie")
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "svc", Namespace: "ns", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer token-123")
	req.Header.Set("Cookie", "session=abc")

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedAuth != "Bearer token-123" {
		t.Fatalf("expected Authorization header, got %q", receivedAuth)
	}
	if receivedCookie != "session=abc" {
		t.Fatalf("expected Cookie header, got %q", receivedCookie)
	}
}

func TestForwardHeadersCustom(t *testing.T) {
	var receivedCustom string
	var receivedAuth string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCustom = r.Header.Get("X-Custom")
		receivedAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "svc", Namespace: "ns", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery, WithForwardHeaders([]string{"X-Custom"}))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Custom", "custom-value")
	req.Header.Set("Authorization", "Bearer should-not-forward")

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedCustom != "custom-value" {
		t.Fatalf("expected X-Custom header, got %q", receivedCustom)
	}
	if receivedAuth != "" {
		t.Fatalf("expected Authorization to be blocked, got %q", receivedAuth)
	}
}

func TestForwardHeadersBlocksUnlisted(t *testing.T) {
	var receivedSecret string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSecret = r.Header.Get("X-Secret")
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "svc", Namespace: "ns", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Secret", "should-not-appear")

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedSecret != "" {
		t.Fatalf("expected X-Secret to be blocked, got %q", receivedSecret)
	}
}
