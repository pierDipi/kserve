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
	"net/url"
)

type Backend struct {
	Name      string
	Namespace string
	URL       *url.URL
	Host      string
	Labels    map[string]string
	Ready     bool
}

type BackendResponse struct {
	Backend Backend
	Body    []byte
	Status  int
	Err     error
}

type BackendDiscovery interface {
	Discover(ctx context.Context) ([]Backend, error)
}

type FilterFunc func(r *http.Request, b Backend) bool

type MergeFunc func(responses []BackendResponse) (any, int, error)

type PartialFailurePolicy int

const (
	FailAll       PartialFailurePolicy = iota
	ReturnPartial
)
