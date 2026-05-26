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
	"net/http"
	"strings"
	"testing"
)

func TestMergeModelsResponsesUnion(t *testing.T) {
	r1, _ := json.Marshal(ListModelsResponse{
		Object: "list",
		Data:   []Model{{ID: "model-a", Object: "model", Created: 1}},
	})
	r2, _ := json.Marshal(ListModelsResponse{
		Object: "list",
		Data:   []Model{{ID: "model-b", Object: "model", Created: 2}},
	})

	responses := []BackendResponse{
		{Backend: Backend{Name: "b1", Namespace: "ns1"}, Body: r1, Status: 200},
		{Backend: Backend{Name: "b2", Namespace: "ns2"}, Body: r2, Status: 200},
	}

	result, status, err := MergeModelsResponses(responses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	resp := result.(ListModelsResponse)
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Data))
	}

	found := make(map[string]string)
	for _, m := range resp.Data {
		found[m.ID] = m.OwnedBy
	}
	if found["model-a"] != "b1/ns1" {
		t.Errorf("model-a OwnedBy = %q, want %q", found["model-a"], "b1/ns1")
	}
	if found["model-b"] != "b2/ns2" {
		t.Errorf("model-b OwnedBy = %q, want %q", found["model-b"], "b2/ns2")
	}
}

func TestMergeModelsResponsesEmpty(t *testing.T) {
	result, status, err := MergeModelsResponses(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	resp := result.(ListModelsResponse)
	if len(resp.Data) != 0 {
		t.Fatalf("expected 0 models, got %d", len(resp.Data))
	}
	if resp.Object != "list" {
		t.Fatalf("expected object 'list', got %s", resp.Object)
	}
}

func TestMergeHealthResponsesAllHealthy(t *testing.T) {
	responses := []BackendResponse{
		{Backend: Backend{Name: "b1", Namespace: "ns"}, Body: []byte(`{}`), Status: 200},
		{Backend: Backend{Name: "b2", Namespace: "ns"}, Body: []byte(`{}`), Status: 200},
	}

	result, status, err := MergeHealthResponses(responses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	hs := result.(HealthStatus)
	if hs.Status != "healthy" {
		t.Fatalf("expected healthy, got %s", hs.Status)
	}
	for _, b := range hs.Backends {
		if b.Status != "healthy" {
			t.Errorf("backend %s expected healthy, got %s", b.Name, b.Status)
		}
	}
}

func TestMergeHealthResponsesPartialFailure(t *testing.T) {
	responses := []BackendResponse{
		{Backend: Backend{Name: "b1", Namespace: "ns"}, Body: []byte(`{}`), Status: 200},
		{Backend: Backend{Name: "b2", Namespace: "ns"}, Err: fmt.Errorf("connection refused")},
	}

	result, status, err := MergeHealthResponses(responses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", status)
	}

	hs := result.(HealthStatus)
	if hs.Status != "unhealthy" {
		t.Fatalf("expected unhealthy, got %s", hs.Status)
	}
}

func TestMergeMetricsResponsesConcatenation(t *testing.T) {
	responses := []BackendResponse{
		{
			Backend: Backend{Name: "b1"},
			Body:    []byte("# HELP requests Total requests\nrequests_total{method=\"GET\"} 100\n"),
			Status:  200,
		},
		{
			Backend: Backend{Name: "b2"},
			Body:    []byte("requests_total{method=\"GET\"} 200\n"),
			Status:  200,
		},
	}

	result, status, err := MergeMetricsResponses(responses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d", status)
	}

	output := result.(string)
	if !strings.Contains(output, `backend="b1"`) {
		t.Error("expected backend label for b1")
	}
	if !strings.Contains(output, `backend="b2"`) {
		t.Error("expected backend label for b2")
	}
	if !strings.Contains(output, "# HELP requests Total requests") {
		t.Error("expected comment line preserved")
	}
}
