# kratos-foundation 开发命令。工具统一安装到 tools/，锁定版本且不影响全局 GOBIN。
# 变量均使用 ?=，可通过命令行或环境变量覆盖，例如 make lint GOLANGCI_LINT_VERSION=v2.15.0。
.DEFAULT_GOAL := help

# ==================== 工具版本与路径 ====================
TOOLS_DIR ?= $(CURDIR)/tools
# protoc 与 protoc-gen-go 版本写入生成文件头，升级会改写全部 *.pb.go。
PROTOC_VERSION ?= 35.1
PROTOC_GEN_GO_VERSION ?= v1.32.0
PROTOC_GEN_VALIDATE_VERSION ?= v1.3.3
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION ?= v1.9.0
# Wire 由 go.mod 的 tool 指令固定，通过 go tool wire 调用，不安装到 tools/。

GOHOSTOS ?= $(shell go env GOHOSTOS)
GOHOSTARCH ?= $(shell go env GOHOSTARCH)
PROTOC := $(TOOLS_DIR)/protoc
GOLANGCI_LINT := $(TOOLS_DIR)/golangci-lint
GOVULNCHECK := $(TOOLS_DIR)/govulncheck
export PATH := $(TOOLS_DIR):$(PATH)

ifeq ($(GOHOSTOS)-$(GOHOSTARCH),darwin-arm64)
PROTOC_PLATFORM := osx-aarch_64
else ifeq ($(GOHOSTOS)-$(GOHOSTARCH),darwin-amd64)
PROTOC_PLATFORM := osx-x86_64
else ifeq ($(GOHOSTOS)-$(GOHOSTARCH),linux-amd64)
PROTOC_PLATFORM := linux-x86_64
else ifeq ($(GOHOSTOS)-$(GOHOSTARCH),linux-arm64)
PROTOC_PLATFORM := linux-aarch_64
endif

# 镜像版本与本地演示环境保持一致。
PROMETHEUS_IMAGE ?= prom/prometheus:v3.5.5
BLACKBOX_IMAGE ?= prom/blackbox-exporter:v0.28.0
ALERTMANAGER_IMAGE ?= prom/alertmanager:v0.28.1
OBSERVABILITY_COMPOSE := docker compose -f test/observability/compose.yaml
COMPONENTS_COMPOSE := $(OBSERVABILITY_COMPOSE) -f test/components/compose.yaml
PROMETHEUS_URL ?= http://127.0.0.1:19090
APP_POD_LABEL ?= pod_name

# ==================== 协议定义 ====================
PROTO_OUT := ./proto/kratos_foundation_pb
CONFIG_PROTO := proto/config.proto
CLIENT_OPTION_PROTO := proto/kratos_foundation_client/client.proto
PROTO_FILES := $(filter-out $(CLIENT_OPTION_PROTO),$(shell find proto -name '*.proto' | sort))

# 根模块与 cmd/ 下的插件模块分别执行 lint/test/vet。
GO_MODULE_DIRS := $(sort $(patsubst %/,%,$(dir $(shell git ls-files --cached --others --exclude-standard -- 'go.mod' '**/go.mod'))))

.PHONY: help
help: ## 列出常用命令
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-26s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ==================== 工具安装 ====================
.PHONY: init reinstall-tools install-protoc
init: install-protoc $(TOOLS_DIR)/protoc-gen-go $(TOOLS_DIR)/protoc-gen-validate $(GOLANGCI_LINT) $(GOVULNCHECK) ## 安装全部开发工具到 tools/（已存在则跳过）

reinstall-tools: ## 按当前版本变量重新安装全部工具（修改版本号后执行）
	rm -rf "$(TOOLS_DIR)"
	@$(MAKE) --no-print-directory init

# 工具以文件为目标：已存在时不重复下载；升级版本需执行 reinstall-tools。
$(TOOLS_DIR)/protoc-gen-go:
	GOBIN="$(TOOLS_DIR)" go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
$(TOOLS_DIR)/protoc-gen-validate:
	GOBIN="$(TOOLS_DIR)" go install github.com/envoyproxy/protoc-gen-validate@$(PROTOC_GEN_VALIDATE_VERSION)
$(GOLANGCI_LINT):
	GOBIN="$(TOOLS_DIR)" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
$(GOVULNCHECK):
	GOBIN="$(TOOLS_DIR)" go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

install-protoc: ## 下载并缓存指定版本的 protoc（版本一致时跳过）
	@set -eu; \
	if [ -x "$(PROTOC)" ] && [ "$$("$(PROTOC)" --version)" = "libprotoc $(PROTOC_VERSION)" ]; then \
		echo "protoc 已缓存: $(PROTOC) (libprotoc $(PROTOC_VERSION))"; exit 0; \
	fi; \
	[ -n "$(PROTOC_PLATFORM)" ] || { echo "不支持的平台: $(GOHOSTOS)/$(GOHOSTARCH)"; exit 1; }; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	curl -fsSL --retry 3 -o "$$tmp/protoc.zip" \
		"https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/protoc-$(PROTOC_VERSION)-$(PROTOC_PLATFORM).zip"; \
	unzip -qo "$$tmp/protoc.zip" -d "$$tmp"; \
	mkdir -p "$(TOOLS_DIR)"; mv -f "$$tmp/bin/protoc" "$(PROTOC)"; chmod +x "$(PROTOC)"; \
	"$(PROTOC)" --version

# ==================== 代码生成 ====================
.PHONY: proto generate
# 仓库内插件每次从当前源码构建，避免复用 PATH 中的旧版本；生成前清理受控产物，防止残留幽灵文件。
proto: install-protoc $(TOOLS_DIR)/protoc-gen-go $(TOOLS_DIR)/protoc-gen-validate ## 生成内部 proto 代码与 config.schema.json
	@set -eu; \
	(cd cmd/protoc-gen-kratos-foundation-errors-v2 && go build -o "$(TOOLS_DIR)/protoc-gen-kratos-foundation-errors-v2" .); \
	(cd cmd/protoc-gen-jsonschema && go build -o "$(TOOLS_DIR)/protoc-gen-jsonschema" .); \
	find ./proto -type f \( -name '*.pb.go' -o -name '*.pb.validate.go' \) -delete; \
	"$(PROTOC)" --proto_path=./proto --proto_path=./third_party \
		--go_out=. --go_opt=module=github.com/jaggerzhuang1994/kratos-foundation/v2 $(CLIENT_OPTION_PROTO); \
	"$(PROTOC)" --proto_path=./third_party \
		--go_out=. --go_opt=module=github.com/jaggerzhuang1994/kratos-foundation/v2 third_party/pubg/jsonschema.proto; \
	"$(PROTOC)" --proto_path=./proto --proto_path=./third_party \
		--go_out=paths=source_relative:$(PROTO_OUT) \
		--kratos-foundation-errors-v2_out=paths=source_relative:$(PROTO_OUT) \
		--validate_out=paths=source_relative,lang=go:$(PROTO_OUT) \
		$(PROTO_FILES); \
	"$(PROTOC)" --proto_path=./proto --proto_path=./third_party \
		--jsonschema_out=. --jsonschema_opt=draft=Draft07 \
		--jsonschema_opt=output_file_suffix=.schema.json \
		--jsonschema_opt=preserve_proto_field_names=true \
		$(CONFIG_PROTO); \
	echo "proto done"

generate: ## 执行 go generate（含示例与测试 fixture 的 Wire 生成）并整理依赖
	go mod tidy
	go generate ./...
	go mod tidy

# ==================== 静态检查 ====================
.PHONY: fmt lint vet vulncheck
fmt: $(GOLANGCI_LINT) ## 按 .golangci.yml 的 formatters 格式化全部模块
	@set -eu; for module in $(GO_MODULE_DIRS); do \
		(cd "$$module" && "$(GOLANGCI_LINT)" fmt --config "$(CURDIR)/.golangci.yml"); \
	done

lint: $(GOLANGCI_LINT) ## 按根 .golangci.yml 检查全部模块
	@set -eu; for module in $(GO_MODULE_DIRS); do \
		echo "> lint $$module"; \
		(cd "$$module" && "$(GOLANGCI_LINT)" run --config "$(CURDIR)/.golangci.yml"); \
	done; echo "lint ok"

vet: ## 逐模块 go vet
	@set -eu; for module in $(GO_MODULE_DIRS); do (cd "$$module" && go vet ./...); done

# 标准库漏洞取决于构建工具链；嵌套插件模块没有 toolchain 指令，统一使用根 go.mod 固定的版本扫描。
ROOT_TOOLCHAIN := $(shell awk '/^toolchain / {print $$2}' go.mod)
vulncheck: $(GOVULNCHECK) ## 用根 go.mod 的工具链逐模块扫描可达的已知漏洞
	@set -eu; for module in $(GO_MODULE_DIRS); do \
		echo "> govulncheck $$module"; \
		(cd "$$module" && GOTOOLCHAIN=$(or $(ROOT_TOOLCHAIN),auto) "$(GOVULNCHECK)" ./...); \
	done

# ==================== 测试 ====================
.PHONY: test race verify test-components test-business test-external test-components-external
# 要求每个含 Go 源文件的包都有可运行的 TestXxx，且手写函数不存在 0% 覆盖。
test: ## 运行全部单元测试并执行包级测试与覆盖门禁
	@set -eu; \
	coverage_dir=$$(mktemp -d); trap 'rm -rf "$$coverage_dir"' EXIT; index=0; \
	for module in $(GO_MODULE_DIRS); do \
		index=$$((index + 1)); \
		missing=$$(cd "$$module" && go list -f '{{if or (gt (len .GoFiles) 0) (gt (len .CgoFiles) 0)}}{{if eq (len .TestGoFiles) 0}}{{if eq (len .XTestGoFiles) 0}}{{.ImportPath}}{{end}}{{end}}{{end}}' ./...); \
		[ -z "$$missing" ] || { echo "packages without unit tests in $$module:"; echo "$$missing"; exit 1; }; \
		packages=$$(cd "$$module" && go list -f '{{if or (gt (len .GoFiles) 0) (gt (len .CgoFiles) 0)}}{{.ImportPath}}{{end}}' ./...); \
		missing=$$(for package in $$packages; do \
			listed=$$(cd "$$module" && go test -run '^$$' -list '^Test' "$$package") || { echo "$$listed" >&2; exit 1; }; \
			echo "$$listed" | grep -Eq '^Test' || echo "$$package"; \
		done); \
		[ -z "$$missing" ] || { echo "packages without runnable TestXxx in $$module:"; echo "$$missing"; exit 1; }; \
		profile="$$coverage_dir/$$index.out"; \
		(cd "$$module" && go test -count=1 -coverprofile="$$profile" ./...); \
		uncovered=$$(cd "$$module" && go tool cover -func="$$profile" | awk '$$NF == "0.0%" && $$1 !~ /\.pb\.go:|\.pb\.validate\.go:|wire_gen\.go:/ {print}'); \
		[ -z "$$uncovered" ] || { echo "handwritten functions without coverage in $$module:"; echo "$$uncovered"; exit 1; }; \
	done

race: ## 逐模块运行竞态检测
	@set -eu; for module in $(GO_MODULE_DIRS); do (cd "$$module" && go test -race ./...); done

verify: test vet race ## 无需外部服务的完整校验（test + vet + race）

test-components: ## 自包含的组件集成用例（配置、事务、客户端、Job、Log、Server、GORM 队列）
	go test -race -count=1 -timeout=2m ./pkg/config ./pkg/database ./pkg/client ./pkg/job ./pkg/log ./pkg/server ./contrib/queue/database/gorm -run '^TestIntegration' -v

test-business: ## 真实 Wire 组装、HTTP/SQLite 业务闭环与生成客户端契约
	go test -count=1 -timeout=3m ./pkg/bootstrap -run '^TestWireAssemblyGeneratesAndRunsCleanup$$' -v
	(cd cmd/protoc-gen-kratos-foundation-client-v2 && go test -count=1 -timeout=3m -run '^TestGeneratedClientsCompileAndReleaseLeasesAgainstPublicFactory$$' -v .)

test-external: ## 在隔离 Docker 中运行真实 MySQL/Redis/Kafka/Consul 集成、基准与剖析
	./test/external/run.sh

test-components-external: ## 同上，只运行功能与竞态用例
	./test/external/run.sh --functional-only

# ==================== 监控面板与规则 ====================
.PHONY: test-observability observability-up observability-down observability-smoke observability-render
test-observability: ## 校验 Prometheus 配置、告警规则测试与 Grafana 面板查询（需要 Docker）
	docker run --rm -v "$(CURDIR)/observability/prometheus:/etc/blackbox:ro" $(BLACKBOX_IMAGE) --config.file=/etc/blackbox/blackbox.yaml --config.check
	$(OBSERVABILITY_COMPOSE) config --quiet
	$(COMPONENTS_COMPOSE) config --quiet
	docker run --rm --entrypoint promtool -v "$(CURDIR):/repo:ro" -w /repo $(PROMETHEUS_IMAGE) check rules observability/prometheus/alerts.yaml
	docker run --rm --entrypoint promtool -v "$(CURDIR):/repo:ro" -w /repo $(PROMETHEUS_IMAGE) check config --syntax-only observability/prometheus/standalone.yaml test/observability/prometheus/prometheus.yaml
	docker run --rm --entrypoint promtool -v "$(CURDIR):/repo:ro" -w /repo $(PROMETHEUS_IMAGE) test rules test/observability/prometheus/alerts.test.yaml
	test/observability/check_alertmanager.sh $(ALERTMANAGER_IMAGE)
	python3 test/observability/check_dashboards.py $(if $(PROMTOOL_CONTAINER),--container $(PROMTOOL_CONTAINER),)

observability-up: ## 启动本地示例应用、Prometheus、Grafana（http://127.0.0.1:13000）
	$(OBSERVABILITY_COMPOSE) up --build -d

observability-down: ## 停止本地监控演示（保留数据卷）
	$(COMPONENTS_COMPOSE) down

observability-smoke: ## 用真实 Prometheus 执行面板查询，例如 PROMETHEUS_URL=http://prom:9090
	python3 test/observability/smoke.py --prometheus-url "$(PROMETHEUS_URL)" --app-pod-label "$(APP_POD_LABEL)" $(if $(COMPONENTS),--components "$(COMPONENTS)",) $(if $(GRAFANA_URL),--grafana-url "$(GRAFANA_URL)",)

observability-render: ## 把面板与规则打包为 ConfigMap/PrometheusRule 到 /tmp/foundation-monitoring（不部署）
	python3 observability/kubernetes/render.py --namespace monitoring --release monitoring --output /tmp/foundation-monitoring

# ==================== 组件演示 ====================
.PHONY: components-up components-exercise
components-up: ## 在监控演示上叠加 components 示例及 Redis、Kafka、OSS 模拟服务
	mkdir -p test/components/.runtime
	cp examples/components/configs/config.yaml test/components/.runtime/config.yaml
	$(COMPONENTS_COMPOSE) up --build -d
	$(COMPONENTS_COMPOSE) kill -s SIGHUP prometheus

components-exercise: ## 驱动 components 示例并核对真实指标增量
	python3 test/components/exercise.py

# ==================== 发布 ====================
.PHONY: changelog verify-release all
changelog: ## 按 Git tag 生成 CHANGELOG 提交记录（保留手写说明）；打 tag 前用 RELEASE=vX.Y.Z 整理发布节
	python3 scripts/changelog.py $(if $(RELEASE),--release $(RELEASE),)

verify-release: verify lint vulncheck test-business ## 本地发布门禁；不打 tag、不推送
	@echo "verify-release ok"

all: proto generate verify lint ## 重新生成并执行全部本地校验
