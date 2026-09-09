package tracing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// newDefaultConfig 根据应用身份和运行环境生成追踪默认值。
func newDefaultConfig(appInfo appinfo.AppInfo) (*config_pb.Tracing, error) {
	if strings.TrimSpace(appInfo.Name()) == "" {
		return nil, errors.New("tracing app name is required")
	}
	defaultDisable := false
	if env.IsLocal() {
		defaultDisable = true
	}

	defaultSample := config_pb.Sampler_RATIO
	defaultCompression := config_pb.Exporter_NO

	return &config_pb.Tracing{
		Disable: proto.Bool(defaultDisable),
		Exporter: &config_pb.Exporter{
			EndpointUrl: proto.String("http://localhost:4318/v1/traces"),
			Compression: &defaultCompression,
			Headers:     nil,
			Timeout:     durationpb.New(10 * time.Second),
			Retry: &config_pb.Exporter_RetryConfig{
				Enabled:         proto.Bool(true),
				InitialInterval: durationpb.New(5 * time.Second),
				MaxInterval:     durationpb.New(30 * time.Second),
				MaxElapsedTime:  durationpb.New(time.Minute),
			},
		},
		Sampler: &config_pb.Sampler{
			Sample: &defaultSample,
			Ratio:  proto.Float64(0.05),
		},
	}, nil
}

// loadConfig 合并追踪默认配置与用户配置，并在建立网络资源前完成校验。
func loadConfig(manager config.Manager, appInfo appinfo.AppInfo) (*config_pb.Tracing, error) {
	defaults, err := newDefaultConfig(appInfo)
	if err != nil {
		return nil, err
	}
	effective := new(config_pb.Tracing)
	if err := manager.Load("tracing", effective, defaults); err != nil {
		return nil, err
	}
	if err := effective.ValidateAll(); err != nil {
		return nil, fmt.Errorf("validate tracing config: %w", err)
	}
	return effective, nil
}
