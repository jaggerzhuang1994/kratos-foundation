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

PROTO_OUT=./proto/kratos_foundation_pb

.PHONY: init
# 初始化框架环境
init:
	go install github.com/google/wire/cmd/wire@latest
	go install github.com/go-kratos/kratos/cmd/kratos/v2@latest
	go install github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v2@latest
	#go install github.com/go-kratos/kratos/cmd/protoc-gen-go-errors/v2@latest
	(cd cmd/protoc-gen-kratos-foundation-errors && go install)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/envoyproxy/protoc-gen-validate@latest
	#go install github.com/google/gnostic/cmd/protoc-gen-openapi@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.27.2
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

.PHONY: proto
# 生成内部 proto
proto:
	@echo "生成 proto..."
	@protoc \
			--proto_path=./proto \
			--proto_path=./third_party \
			--go_out=paths=source_relative:$(PROTO_OUT) \
			--kratos-foundation-errors_out=paths=source_relative:$(PROTO_OUT) \
			--validate_out=paths=source_relative,lang=go:$(PROTO_OUT) \
			$(PROTO_FILES)

.PHONY: generate
generate:
	@echo "生成 generate..."
	@go generate ./...

.PHONY: all
all: generate proto

.PHONY: lint
# 代码审查
lint:
	@golangci-lint run
