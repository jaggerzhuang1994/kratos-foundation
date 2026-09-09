package main

import (
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

// main 解析命令行参数，并把 protoc 请求交给 run 处理。
func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-kratos-foundation-client-v2 %v\n", Version)
		return
	}

	var flags flag.FlagSet
	protogen.Options{
		ParamFunc: flags.Set,
	}.Run(run)
}

// run 处理一次 protoc 生成请求，让错误由 protoc 协议统一报告。
func run(gen *protogen.Plugin) error {
	gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	for _, file := range gen.Files {
		if !file.Generate {
			continue
		}
		if err := generateFile(gen, file); err != nil {
			return err
		}
	}
	return nil
}
