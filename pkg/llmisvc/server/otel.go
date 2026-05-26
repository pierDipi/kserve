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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/kserve/kserve/pkg/llmisvc/server"

func getTracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

func getMeter() otelmetric.Meter {
	return otel.Meter(instrumentationName)
}

func recordFloat64Histogram(ctx context.Context, name, desc, unit string, value float64, attrs ...attribute.KeyValue) {
	h, err := getMeter().Float64Histogram(name,
		otelmetric.WithDescription(desc),
		otelmetric.WithUnit(unit),
	)
	if err != nil {
		otel.Handle(err)
		return
	}
	h.Record(ctx, value, otelmetric.WithAttributes(attrs...))
}

func recordInt64Histogram(ctx context.Context, name, desc string, value int64, attrs ...attribute.KeyValue) {
	h, err := getMeter().Int64Histogram(name,
		otelmetric.WithDescription(desc),
	)
	if err != nil {
		otel.Handle(err)
		return
	}
	h.Record(ctx, value, otelmetric.WithAttributes(attrs...))
}

func addInt64Counter(ctx context.Context, name, desc string, value int64, attrs ...attribute.KeyValue) {
	c, err := getMeter().Int64Counter(name,
		otelmetric.WithDescription(desc),
	)
	if err != nil {
		otel.Handle(err)
		return
	}
	c.Add(ctx, value, otelmetric.WithAttributes(attrs...))
}

func otelSpanAttrs(attrs ...attribute.KeyValue) trace.SpanStartOption {
	return trace.WithAttributes(attrs...)
}
