deps:
	go install github.com/pseudomuto/protoc-gen-doc/cmd/protoc-gen-doc@latest

options:
	@echo "> protoc options..."
	@protoc \
	--proto_path=./ \
	--proto_path=./third_party \
	--go_out=./pkg/proto \
	--go_opt=paths=source_relative \
	--doc_out=. \
	--doc_opt=markdown,options.md \
	./*.proto && echo "done."
