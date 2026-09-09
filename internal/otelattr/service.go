package otelattr

import (
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
)

// ServiceAttributes 统一生成资源身份，避免各组件对同一组标签产生不同解释。
func ServiceAttributes(info appinfo.AppInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.ServiceNameKey.String(info.Name()),
		semconv.ServiceInstanceIDKey.String(info.ID()),
		semconv.ServiceVersionKey.String(info.Version()),
	}
}
