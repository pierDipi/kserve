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
)

type Aggregator struct {
	Discovery     BackendDiscovery
	Filter        FilterFunc
	Timeout       time.Duration
	FailurePolicy PartialFailurePolicy
	HTTPClient    *http.Client
}

func NewAggregator(discovery BackendDiscovery, opts ...Option) *Aggregator {
	a := &Aggregator{
		Discovery:     discovery,
		Timeout:       10 * time.Second,
		FailurePolicy: ReturnPartial,
		HTTPClient:    http.DefaultClient,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Aggregator) FanOut(ctx context.Context, r *http.Request, path string, merge MergeFunc) (any, int, error) {
	backends, err := a.Discovery.Discover(ctx)
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("discovering backends: %w", err)
	}

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
			resp := a.queryBackend(ctx, backend, path)
			mu.Lock()
			responses = append(responses, resp)
			mu.Unlock()
		}(b)
	}

	wg.Wait()

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

func (a *Aggregator) queryBackend(ctx context.Context, backend Backend, path string) BackendResponse {
	ctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()

	targetURL := *backend.URL
	targetURL.Path = path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		slog.Error("failed to create request", "backend", backend.Name, "error", err)
		return BackendResponse{Backend: backend, Err: err}
	}

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		slog.Error("failed to query backend", "backend", backend.Name, "error", err)
		return BackendResponse{Backend: backend, Err: err}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body", "backend", backend.Name, "error", err)
		return BackendResponse{Backend: backend, Err: err}
	}

	return BackendResponse{
		Backend: backend,
		Body:    body,
		Status:  resp.StatusCode,
	}
}
