ROOT := ../..

.PHONY: help generate build test run
help:
	@echo 'generate: 用根模块固定的 Wire 生成组装代码'
	@echo 'build: 构建 /tmp/foundation-components-api'
	@echo 'test: 验证示例公共 API 和真实 HTTP 指标契约'
	@echo 'run: 在仓库根目录运行示例（先配置可达的依赖地址）'
	@echo 'up: 启动本地组件及 Grafana/Prometheus'
	@echo 'exercise: 运行五轮业务并核对真实指标'
	@echo 'down: 停止整个本地演示项目（保留监控卷）'

generate:
	cd $(ROOT) && go tool wire ./examples/components/cmd/api

build:
	cd $(ROOT) && go build -o /tmp/foundation-components-api ./examples/components/cmd/api

test:
	cd $(ROOT) && go test -race ./examples/components/cmd/api
	cd $(ROOT) && go vet ./examples/components/cmd/api

run:
	cd $(ROOT) && LOG_FILE_ENABLE=false go run ./examples/components/cmd/api

COMPOSE = docker compose -f $(ROOT)/deploy/observability/compose.yaml -f compose.yaml
.PHONY: up down exercise
up:
	mkdir -p .runtime
	cp configs/config.yaml .runtime/config.yaml
	$(COMPOSE) up --build -d
	$(COMPOSE) kill -s SIGHUP prometheus
down:
	$(COMPOSE) down
exercise:
	python3 exercise.py
