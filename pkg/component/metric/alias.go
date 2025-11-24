package metric

import "go.opentelemetry.io/otel/metric"

var WithUnit = metric.WithUnit
var WithDescription = metric.WithDescription
var WithExplicitBucketBoundaries = metric.WithExplicitBucketBoundaries
var _ = WithUnit
var _ = WithDescription
var _ = WithExplicitBucketBoundaries
