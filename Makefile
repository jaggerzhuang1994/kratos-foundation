# 扫描 PROTO_FILES 内部proto文件列表
ifeq ($(GOHOSTOS), windows)
	#the `find.exe` is different from `find` in bash/shell.
	#to see https://docs.microsoft.com/en-us/windows-server/administration/windows-commands/find.
	#changed to use git-bash.exe to run find cli or other cli friendly, caused of every developer has a Git.
	#Git_Bash= $(subst cmd\,bin\bash.exe,$(dir $(shell where git)))
	Git_Bash=$(subst \,/,$(subst cmd\,bin\bash.exe,$(dir $(shell where git))))
	PROTO_FILES=$(shell $(Git_Bash) -c "find proto -name *.proto")
else
	PROTO_FILES=$(shell find proto -name '*.proto' | sort)
endif

PROTO_OUT=./proto/kratos_foundation_pb
CONFIG_PROTO=proto/config.proto
CLIENT_OPTION_PROTO=proto/kratos_foundation_client/client.proto
PROTO_FILES:=$(filter-out $(CLIENT_OPTION_PROTO),$(PROTO_FILES))

WIRE_VERSION ?= v0.7.0
PROTOC_GEN_GO_VERSION ?= v1.32.0
PROTOC_GEN_GO_GRPC_VERSION ?= v1.3.0
KRATOS_VERSION ?= v2.9.2
PROTOC_GEN_VALIDATE_VERSION ?= v1.2.1
GRPC_GATEWAY_VERSION ?= v2.27.3
GOLANGCI_LINT_VERSION ?= v2.11.4

GO_MODULE_FILES := $(shell git ls-files --cached --others --exclude-standard -- 'go.mod' '**/go.mod')
GO_MODULE_DIRS := $(sort $(patsubst %/,%,$(dir $(GO_MODULE_FILES))))

.PHONY: init-proto
# 安装 proto 生成所需的固定版本第三方插件；仓库内插件由 proto 目标直接构建。
init-proto:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install github.com/envoyproxy/protoc-gen-validate@$(PROTOC_GEN_VALIDATE_VERSION)

.PHONY: init-lint
# 单独安装 lint 工具，让 CI 不必拉取全部生成器也能使用与本地相同的固定版本。
init-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: init
# 初始化框架环境
init: init-proto init-lint
	go install github.com/google/wire/cmd/wire@$(WIRE_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	go install github.com/go-kratos/kratos/cmd/kratos/v2@$(KRATOS_VERSION)
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@$(KRATOS_VERSION)
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@$(GRPC_GATEWAY_VERSION)
	(cd cmd/protoc-gen-kratos-foundation-errors-v2 && go install)
	(cd cmd/protoc-gen-kratos-foundation-client-v2 && go install)
	(cd cmd/protoc-gen-jsonschema && go install)

.PHONY: generate
generate:
	@echo "> 生成 generate..."
	@go mod tidy
	@go generate ./...
	@go mod tidy
	@echo "done"

.PHONY: proto
# 生成内部 proto；自定义插件必须由当前工作树构建，不能复用 PATH 中的旧版本。
# 生成前清理受控产物，避免已删除的 proto 或错误枚举留下幽灵 Go 文件。
proto:
	@set -eu; \
		tool_dir=$$(mktemp -d "$${TMPDIR:-/tmp}/kratos-foundation-proto.XXXXXX"); \
		trap 'rm -rf "$$tool_dir"' EXIT HUP INT TERM; \
		command -v protoc >/dev/null; \
		command -v protoc-gen-go >/dev/null; \
		command -v protoc-gen-validate >/dev/null; \
		(cd cmd/protoc-gen-kratos-foundation-errors-v2 && go build -o "$$tool_dir/protoc-gen-kratos-foundation-errors-v2" .); \
		(cd cmd/protoc-gen-jsonschema && go build -o "$$tool_dir/protoc-gen-jsonschema" .); \
		echo "> 生成 proto..."; \
		find ./proto -type f \( -name '*.pb.go' -o -name '*.pb.validate.go' \) -delete; \
		protoc \
			--proto_path=./proto \
			--proto_path=./third_party \
			--go_out=. \
			--go_opt=module=github.com/jaggerzhuang1994/kratos-foundation/v2 \
			$(CLIENT_OPTION_PROTO); \
		protoc \
			--proto_path=./third_party \
			--go_out=. \
			--go_opt=module=github.com/jaggerzhuang1994/kratos-foundation/v2 \
			third_party/pubg/jsonschema.proto; \
		protoc \
			--proto_path=./proto \
			--proto_path=./third_party \
			--go_out=paths=source_relative:$(PROTO_OUT) \
			--plugin=protoc-gen-kratos-foundation-errors-v2="$$tool_dir/protoc-gen-kratos-foundation-errors-v2" \
			--kratos-foundation-errors-v2_out=paths=source_relative:$(PROTO_OUT) \
			--validate_out=paths=source_relative,lang=go:$(PROTO_OUT) \
			$(PROTO_FILES); \
		echo 'done'; \
		echo "> 生成 config.schema.json..."; \
		protoc \
			--proto_path=./proto \
			--proto_path=./third_party \
			--plugin=protoc-gen-jsonschema="$$tool_dir/protoc-gen-jsonschema" \
			--jsonschema_out=. \
			--jsonschema_opt=draft=Draft07 \
			--jsonschema_opt=output_file_suffix=.schema.json \
			--jsonschema_opt=preserve_proto_field_names=true \
			$(CONFIG_PROTO); \
		echo 'done'

.PHONY: lint
# 按模块执行相同规则，避免根模块的 ./... 漏掉嵌套生成器模块。
lint:
	@set -eu; \
		for module in $(GO_MODULE_DIRS); do \
			echo "> lint $$module..."; \
			(cd "$$module" && golangci-lint run --config "$(CURDIR)/.golangci.yml"); \
		done; \
		echo 'lint ok'

.PHONY: test-components
# 自包含组件集成用例；真实 Kafka/Redis 另见 test-components-external。
test-components:
	go test -race -count=1 -timeout=2m ./pkg/config ./pkg/database ./pkg/client ./pkg/job ./pkg/log ./pkg/server ./contrib/queue/database/gorm -run '^TestIntegration' -v

.PHONY: test-components-external
# 复用隔离服务生命周期，只运行功能与竞态用例，省略批量基准和剖析。
test-components-external:
	./scripts/test-external.sh --functional-only

.PHONY: test-business
# 真实 Wire 组装、HTTP/SQLite 业务闭环及生成客户端契约；强制重新执行。
test-business:
	go test -count=1 -timeout=3m ./pkg/bootstrap -run '^TestWireAssemblyGeneratesAndRunsCleanup$$' -v
	(cd cmd/protoc-gen-kratos-foundation-client-v2 && go test -count=1 -timeout=3m -run '^TestGeneratedClientsCompileAndReleaseLeasesAgainstPublicFactory$$' -v .)

.PHONY: verify-release
# 本地发布门禁；不会打 tag、推送或部署。
verify-release:
	$(MAKE) verify
	$(MAKE) lint
	$(MAKE) test-business

.PHONY: test
# 逐模块检查每个包都有独立测试、每个手写函数都被直接执行，并运行全部单元测试。
test:
	@set -eu; \
		coverage_dir=$$(mktemp -d "$${TMPDIR:-/tmp}/kratos-foundation-cover.XXXXXX"); \
		trap 'rm -rf "$$coverage_dir"' EXIT HUP INT TERM; \
		module_index=0; \
		for module in $(GO_MODULE_DIRS); do \
			module_index=$$((module_index + 1)); \
			missing=$$(cd "$$module" && go list -f '{{if or (gt (len .GoFiles) 0) (gt (len .CgoFiles) 0)}}{{if eq (len .TestGoFiles) 0}}{{if eq (len .XTestGoFiles) 0}}{{.ImportPath}}{{end}}{{end}}{{end}}' ./...); \
			if [ -n "$$missing" ]; then \
				echo "packages without independent unit tests in $$module:"; \
				echo "$$missing"; \
				exit 1; \
			fi; \
			packages=$$(cd "$$module" && go list -f '{{if or (gt (len .GoFiles) 0) (gt (len .CgoFiles) 0)}}{{.ImportPath}}{{end}}' ./...); \
			missing=$$(for package in $$packages; do \
				if ! listed=$$(cd "$$module" && go test -run '^$$' -list '^Test' "$$package"); then \
					echo "$$listed" >&2; \
					exit 1; \
				fi; \
				if ! echo "$$listed" | grep -Eq '^Test'; then \
					echo "$$package"; \
				fi; \
			done); \
			if [ -n "$$missing" ]; then \
				echo "packages without runnable TestXxx unit tests in $$module:"; \
				echo "$$missing"; \
				exit 1; \
			fi; \
			coverage_file="$$coverage_dir/module-$$module_index.out"; \
			coverage_report="$$coverage_dir/module-$$module_index.func"; \
			(cd "$$module" && go test -count=1 -coverprofile="$$coverage_file" ./...); \
			(cd "$$module" && go tool cover -func="$$coverage_file") > "$$coverage_report"; \
			uncovered=$$(awk '$$NF == "0.0%" && $$1 !~ /\.pb\.go:/ && $$1 !~ /\.pb\.validate\.go:/ && $$1 !~ /wire_gen\.go:/ {print}' "$$coverage_report"); \
			if [ -n "$$uncovered" ]; then \
				echo "handwritten functions without direct unit-test coverage in $$module:"; \
				echo "$$uncovered"; \
				exit 1; \
			fi; \
		done

.PHONY: vet
# 逐模块运行 Go 静态检查。
vet:
	@set -eu; \
		for module in $(GO_MODULE_DIRS); do \
			(cd "$$module" && go vet ./...); \
		done

.PHONY: race
# 逐模块使用 race detector 运行测试。
race:
	@set -eu; \
		for module in $(GO_MODULE_DIRS); do \
			(cd "$$module" && go test -race ./...); \
		done

.PHONY: verify
# 运行无需外部基础设施的完整校验
verify: test vet race

.PHONY: all
all: proto generate verify lint

.PHONY: test-external
# 启动隔离 Docker 服务，执行真实集成、批量基准和剖析；结束时清理本次容器及数据。
test-external:
	./scripts/test-external.sh
