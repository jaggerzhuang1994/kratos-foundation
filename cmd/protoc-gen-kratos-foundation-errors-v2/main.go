package main

import (
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"
)

const defaultErrorStackSkip = 4

// main 解析命令行参数，并把 protoc 请求交给 run 处理。
func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-kratos-foundation-errors-v2 %v\n", Version)
		return
	}
	var flags flag.FlagSet
	errorStackSkip := registerGeneratorFlags(&flags)
	protogen.Options{
		ParamFunc: flags.Set,
	}.Run(func(gen *protogen.Plugin) error {
		return run(gen, *errorStackSkip)
	})
}

// registerGeneratorFlags 注册 protoc 通过生成器 option 传入的参数。
func registerGeneratorFlags(flags *flag.FlagSet) *int {
	return flags.Int(
		"stack_skip",
		defaultErrorStackSkip,
		"number of stack frames skipped by generated error helpers",
	)
}

// run 处理一次 protoc 生成请求，让校验错误由 protoc 协议统一报告。
func run(gen *protogen.Plugin, errorStackSkip int) error {
	if errorStackSkip < 0 {
		return fmt.Errorf("stack_skip must be non-negative, got %d", errorStackSkip)
	}
	gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
	for _, file := range gen.Files {
		if !file.Generate {
			continue
		}
		if err := generateFile(gen, file, errorStackSkip); err != nil {
			return err
		}
	}
	return nil
}
