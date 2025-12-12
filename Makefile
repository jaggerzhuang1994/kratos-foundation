# 扫描 PROTO_FILES 内部proto文件列表
ifeq ($(GOHOSTOS), windows)
	#the `find.exe` is different from `find` in bash/shell.
	#to see https://docs.microsoft.com/en-us/windows-server/administration/windows-commands/find.
	#changed to use git-bash.exe to run find cli or other cli friendly, caused of every developer has a Git.
	#Git_Bash= $(subst cmd\,bin\bash.exe,$(dir $(shell where git)))
	Git_Bash=$(subst \,/,$(subst cmd\,bin\bash.exe,$(dir $(shell where git))))
	PROTO_FILES=$(shell $(Git_Bash) -c "find proto -name *.proto")
else
	PROTO_FILES=$(shell find proto -name *.proto)
endif

GO_MODULE=$(shell go list -m)
VERSION=$(shell git describe --tags --always)

.PHONY: init
# 初始化框架环境
init:
	go install github.com/google/wire/cmd/wire@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.32.0
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.3.0
	go install github.com/go-kratos/kratos/cmd/kratos/v2@latest
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@latest
	go install github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-kratos-foundation-errors@main
	go install github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-kratos-foundation-client@main
	go install github.com/envoyproxy/protoc-gen-validate@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

.PHONY: generate
generate:
	@echo "> 生成 generate..."
	@go mod tidy
	@go generate ./...
	@go mod tidy
	@echo "done"

PROTO_OUT=./proto/kratos_foundation_pb

.PHONY: proto
# 生成内部 proto
proto:
	@echo "> 生成 proto..."
	@protoc \
			--proto_path=./proto \
			--proto_path=./third_party \
			--go_out=paths=source_relative:$(PROTO_OUT) \
			--kratos-foundation-errors_out=paths=source_relative:$(PROTO_OUT) \
			--validate_out=paths=source_relative,lang=go:$(PROTO_OUT) \
			$(PROTO_FILES) && echo 'done'

.PHONY: lint
# 代码审查
lint:
	@echo "> lint..."
	@golangci-lint run && echo 'lint ok'

.PHONY: all
all: generate proto lint
