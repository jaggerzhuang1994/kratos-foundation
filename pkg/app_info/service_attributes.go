package app_info

import (
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type ServiceAttributes []attribute.KeyValue

func NewServiceAttributes(
	appInfo AppInfo,
) ServiceAttributes {
	return ServiceAttributes{
		semconv.ServiceNameKey.String(appInfo.GetName()),
		semconv.ServiceInstanceIDKey.String(appInfo.GetId()),
		semconv.ServiceVersionKey.String(appInfo.GetVersion()),
	}
}
