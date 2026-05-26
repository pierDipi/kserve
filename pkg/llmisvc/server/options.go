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
	"net/http"
	"time"
)

type Option func(*Aggregator)

func WithTimeout(d time.Duration) Option {
	return func(a *Aggregator) {
		a.Timeout = d
	}
}

func WithFilter(f FilterFunc) Option {
	return func(a *Aggregator) {
		a.Filter = f
	}
}

func WithFailurePolicy(p PartialFailurePolicy) Option {
	return func(a *Aggregator) {
		a.FailurePolicy = p
	}
}

func WithHTTPClient(c *http.Client) Option {
	return func(a *Aggregator) {
		a.HTTPClient = c
	}
}

func WithForwardHeaders(headers []string) Option {
	return func(a *Aggregator) {
		a.ForwardHeaders = headers
	}
}
