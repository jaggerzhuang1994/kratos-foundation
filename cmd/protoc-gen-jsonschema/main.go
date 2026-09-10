package main

import (
	"fmt"
	"os"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/modules"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/types/pluginpb"
)

var Version = "develop"

const helpMessage = `Usage protoc-gen-jsonschema:
本仓库维护的 JSON Schema 生成器，支持 draft-04、draft-06、draft-07、draft-2019-09、draft-2020-12。
请从本仓库 cmd/protoc-gen-jsonschema 构建或使用根目录 make init 安装；将生成的二进制放入 PATH。
以下命令在业务 proto 目录执行，Config 必须是 config.proto 中的顶层消息：

protoc --jsonschema_out=. --jsonschema_opt=entrypoint_message=Config config.proto

也可通过 pubg.jsonschema.file 的 entrypoint_message 配置入口，文件级非空值优先。
没有入口或找不到消息时，会提示并跳过该文件，不生成 schema。

在上述命令上追加选项：
  --jsonschema_opt=output_file_suffix=.yaml       输出 YAML
  --jsonschema_opt=pretty_json_output=false       输出紧凑 JSON
  --jsonschema_opt=respect_protojson_int64=true    将 int64 系列字段映射为字符串
  --jsonschema_opt=preserve_proto_field_names=true 保留 proto 字段名
  --jsonschema_opt=mandatory_nullable=false       为 optional/真实 oneof 成员添加 null

自定义选项需导入 pubg/jsonschema.proto，并传入 --proto_path=<仓库>/third_party。
完整安装与字段选项示例见本仓库 cmd/protoc-gen-jsonschema/README.md。

FLAGS:
  --version  : print version
  --help     : print help
`

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Println(Version)
		case "--help":
			fmt.Print(helpMessage)
		}
		return
	}

	feature := uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	pgs.Init(
		pgs.DebugEnv("DEBUG"),
		pgs.SupportedFeatures(&feature)).
		RegisterModule(modules.NewModule()).
		Render()
}
