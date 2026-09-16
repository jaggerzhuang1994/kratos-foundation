package main

import (
	"fmt"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/file"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/registry/consul"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type configPath string

func newSpec(application *app.Spec, servers *server.Spec, jobs *job.Spec, path configPath) (*bootstrap.Spec, error) {
	// 模板要求一个存在的文件，避免文件源未匹配时仅告警并使用默认配置启动。
	info, err := os.Stat(string(path))
	if err != nil {
		return nil, fmt.Errorf("stat configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("configuration must be a regular file")
	}
	spec := bootstrap.NewSpec(application, servers, jobs, bootstrap.ConfigSources{}).Configuration(file.AddConfigSource(string(path)))
	return spec, nil
}

func boot(_ bootstrap.InfrastructureBootstrap, spec *bootstrap.Spec, service *greetingService) (bootstrap.Bootstrap, error) {
	spec.Http().Register(func(srv server.HTTPServer) error {
		service.register(srv)
		return nil
	})
	return bootstrap.Bootstrap{}, nil
}
