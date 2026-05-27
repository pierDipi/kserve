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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
)

// DefaultForwardHeaders is the default set of HTTP headers forwarded from
// the incoming request to each backend request.
var DefaultForwardHeaders = []string{
	"Authorization",
	"Cookie",
	"X-Request-ID",
	// W3C Trace Context
	"traceparent",
	"tracestate",
	// Zipkin B3
	"X-B3-TraceId",
	"X-B3-SpanId",
	"X-B3-ParentSpanId",
	"X-B3-Sampled",
	"X-B3-Flags",
}

type Aggregator struct {
	Discovery      BackendDiscovery
	Filter         FilterFunc
	Timeout        time.Duration
	FailurePolicy  PartialFailurePolicy
	HTTPClient     *http.Client
	ForwardHeaders []string
}

func NewAggregator(discovery BackendDiscovery, opts ...Option) *Aggregator {
	a := &Aggregator{
		Discovery:      discovery,
		Timeout:        10 * time.Second,
		FailurePolicy:  ReturnPartial,
		HTTPClient:     http.DefaultClient,
		ForwardHeaders: DefaultForwardHeaders,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Aggregator) FanOut(ctx context.Context, r *http.Request, path string, merge MergeFunc) (any, int, error) {
	ctx, span := getTracer().Start(ctx, "FanOut", otelSpanAttrs(attribute.String("path", path)))
	defer span.End()

	start := time.Now()

	backends, err := a.Discovery.Discover(ctx)
	discoveryDuration := time.Since(start).Seconds()
	recordFloat64Histogram(ctx, "llmisvc.discovery.duration", "Backend discovery duration in seconds", "s", discoveryDuration)
	recordInt64Histogram(ctx, "llmisvc.discovery.backends", "Number of backends discovered", int64(len(backends)))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "discovery failed")
		return nil, http.StatusInternalServerError, fmt.Errorf("discovering backends: %w", err)
	}

	span.SetAttributes(attribute.Int("backends.discovered", len(backends)))

	var filtered []Backend
	for _, b := range backends {
		if a.Filter != nil && !a.Filter(r, b) {
			continue
		}
		filtered = append(filtered, b)
	}

	if len(filtered) == 0 {
		return merge(nil)
	}

	var (
		mu        sync.Mutex
		wg        sync.WaitGroup
		responses = make([]BackendResponse, 0, len(filtered))
	)

	for _, b := range filtered {
		wg.Add(1)
		go func(backend Backend) {
			defer wg.Done()
			resp := a.queryBackend(ctx, r, backend, path)
			mu.Lock()
			responses = append(responses, resp)
			mu.Unlock()
		}(b)
	}

	wg.Wait()

	fanoutDuration := time.Since(start).Seconds()
	recordFloat64Histogram(ctx, "llmisvc.fanout.duration", "Total fan-out wall time in seconds", "s", fanoutDuration, attribute.String("path", path))

	if a.FailurePolicy == FailAll {
		for _, resp := range responses {
			if resp.Err != nil {
				return nil, http.StatusBadGateway, fmt.Errorf("backend %s/%s failed: %w", resp.Backend.Namespace, resp.Backend.Name, resp.Err)
			}
			if resp.Status >= 500 {
				return nil, http.StatusBadGateway, fmt.Errorf("backend %s/%s returned status %d", resp.Backend.Namespace, resp.Backend.Name, resp.Status)
			}
		}
	}

	return merge(responses)
}

func (a *Aggregator) queryBackend(ctx context.Context, incomingReq *http.Request, backend Backend, path string) BackendResponse {
	ctx, span := getTracer().Start(ctx, "queryBackend", otelSpanAttrs(
		attribute.String("backend.name", backend.Name),
		attribute.String("backend.namespace", backend.Namespace),
		attribute.String("backend.url", backend.URL.String()),
	))
	defer span.End()

	ctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()

	targetURL := *backend.URL
	targetURL.Path = path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		slog.Error("failed to create request", "backend", backend.Name, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create request")
		return BackendResponse{Backend: backend, Err: err}
	}

	if backend.Host != "" {
		req.Host = backend.Host
	}

	for _, h := range a.ForwardHeaders {
		if v := incomingReq.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	backendAttrs := []attribute.KeyValue{
		attribute.String("backend.name", backend.Name),
		attribute.String("backend.namespace", backend.Namespace),
		attribute.String("path", path),
	}

	start := time.Now()
	resp, err := a.HTTPClient.Do(req)
	duration := time.Since(start).Seconds()

	if err != nil {
		slog.Error("failed to query backend", "backend", backend.Name, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "request failed")
		addInt64Counter(ctx, "llmisvc.backend.request.errors", "Backend request errors", 1, backendAttrs...)
		recordFloat64Histogram(ctx, "llmisvc.backend.request.duration", "Per-backend request duration in seconds", "s", duration, backendAttrs...)
		return BackendResponse{Backend: backend, Err: err}
	}
	defer resp.Body.Close()

	backendAttrs = append(backendAttrs, attribute.Int("http.status_code", resp.StatusCode))
	recordFloat64Histogram(ctx, "llmisvc.backend.request.duration", "Per-backend request duration in seconds", "s", duration, backendAttrs...)
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	if resp.StatusCode >= 500 {
		addInt64Counter(ctx, "llmisvc.backend.request.errors", "Backend request errors", 1, backendAttrs...)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "backend", backend.Name, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to read body")
		return BackendResponse{Backend: backend, Err: err}
	}

	return BackendResponse{
		Backend: backend,
		Body:    body,
		Status:  resp.StatusCode,
	}
}
