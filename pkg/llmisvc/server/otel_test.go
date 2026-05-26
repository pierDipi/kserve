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
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTraceContextPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	origTP := otel.GetTracerProvider()
	origProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer func() {
		otel.SetTracerProvider(origTP)
		otel.SetTextMapPropagator(origProp)
	}()

	var receivedTraceparent string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("traceparent")
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "svc", Namespace: "ns", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedTraceparent == "" {
		t.Fatal("expected traceparent header to be injected into backend request")
	}

	spans := exporter.GetSpans()
	if len(spans) < 2 {
		t.Fatalf("expected at least 2 spans (FanOut + queryBackend), got %d", len(spans))
	}

	spanNames := make(map[string]bool)
	for _, s := range spans {
		spanNames[s.Name] = true
	}
	if !spanNames["FanOut"] {
		t.Error("expected a span named FanOut")
	}
	if !spanNames["queryBackend"] {
		t.Error("expected a span named queryBackend")
	}
}

func TestSpansRecordBackendAttributes(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	origTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(origTP)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	discovery := NewStaticDiscovery([]Backend{
		{Name: "my-backend", Namespace: "prod", URL: u, Ready: true},
	})

	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()

	var querySpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "queryBackend" {
			querySpan = &spans[i]
			break
		}
	}
	if querySpan == nil {
		t.Fatal("expected queryBackend span")
	}

	attrs := make(map[string]string)
	for _, a := range querySpan.Attributes {
		attrs[string(a.Key)] = a.Value.Emit()
	}

	if attrs["backend.name"] != "my-backend" {
		t.Errorf("expected backend.name=my-backend, got %q", attrs["backend.name"])
	}
	if attrs["backend.namespace"] != "prod" {
		t.Errorf("expected backend.namespace=prod, got %q", attrs["backend.namespace"])
	}
}

func TestFanOutSpanRecordsPath(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer tp.Shutdown(context.Background())

	origTP := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(origTP)

	discovery := NewStaticDiscovery([]Backend{})
	agg := NewAggregator(discovery)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	_, _, err := agg.FanOut(req.Context(), req, "/v1/models", MergeModelsResponses)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spans := exporter.GetSpans()

	var fanoutSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "FanOut" {
			fanoutSpan = &spans[i]
			break
		}
	}
	if fanoutSpan == nil {
		t.Fatal("expected FanOut span")
	}

	attrs := make(map[string]string)
	for _, a := range fanoutSpan.Attributes {
		attrs[string(a.Key)] = a.Value.Emit()
	}
	if attrs["path"] != "/v1/models" {
		t.Errorf("expected path=/v1/models, got %q", attrs["path"])
	}
}
