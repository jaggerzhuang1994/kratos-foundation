PROTOC_GEN_DOC_VERSION ?= v1.5.1

.PHONY: deps options
deps:
	go install github.com/pseudomuto/protoc-gen-doc/cmd/protoc-gen-doc@$(PROTOC_GEN_DOC_VERSION)

options:
	@echo "> protoc options..."
	@protoc \
	--proto_path=../../third_party \
	--doc_out=. \
	--doc_opt=markdown,options.md \
	../../third_party/pubg/jsonschema.proto && echo "done."
