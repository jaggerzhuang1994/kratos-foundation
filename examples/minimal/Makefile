ROOT := ../..

.PHONY: help generate build test run
help:
	@echo 'generate: 用根模块固定的 Wire 生成组装代码'
	@echo 'build: 构建 /tmp/foundation-minimal-api'
	@echo 'test: 验证示例公共 API 和真实 HTTP 指标契约'
	@echo 'run: 在仓库根目录运行示例'

generate:
	cd $(ROOT) && go tool wire ./examples/minimal/cmd/api

build:
	cd $(ROOT) && go build -o /tmp/foundation-minimal-api ./examples/minimal/cmd/api

test:
	cd $(ROOT) && go test -race ./examples/minimal/cmd/api
	cd $(ROOT) && go vet ./examples/minimal/cmd/api

run:
	cd $(ROOT) && LOG_FILE_DISABLE=true go run ./examples/minimal/cmd/api
