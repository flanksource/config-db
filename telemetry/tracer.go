package telemetry

import (
	"context"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/credentials"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func InitTracer(serviceName, collectorURL string, insecure bool) func(context.Context) error {
	var secureOption otlptracegrpc.Option
	if !insecure {
		secureOption = otlptracegrpc.WithTLSCredentials(credentials.NewClientTLSFromCert(nil, ""))
	} else {
		secureOption = otlptracegrpc.WithInsecure()
	}

	exporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			secureOption,
			otlptracegrpc.WithEndpoint(collectorURL),
		),
	)

	if err != nil {
		logger.Errorf("failed to create opentelemetry exporter: %v", err)
		return func(_ context.Context) error { return nil }
	}

	attributes := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if val, ok := os.LookupEnv("OTEL_LABELS"); ok {
		kv := collections.KeyValueSliceToMap(strings.Split(val, ","))
		for k, v := range kv {
			attributes = append(attributes, attribute.String(k, v))
		}
	}

	resources, err := resource.New(context.Background(), resource.WithAttributes(attributes...))
	if err != nil {
		logger.Errorf("could not set opentelemetry resources: %v", err)
		return func(_ context.Context) error { return nil }
	}

	otel.SetTracerProvider(
		sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(resources),
		),
	)

	// Register the TraceContext propagator globally.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return exporter.Shutdown
}
