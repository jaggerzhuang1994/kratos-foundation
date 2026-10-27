package main

import (
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/servicename"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

var (
	contextPackage = protogen.GoImportPath("context")
	fmtPackage     = protogen.GoImportPath("fmt")
	wirePackage    = protogen.GoImportPath("github.com/google/wire")
	clientPackage  = protogen.GoImportPath("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client")
)

// generateFile 为含有服务的 Proto 文件生成 Factory 客户端适配层。
func generateFile(gen *protogen.Plugin, file *protogen.File) error {
	if len(file.Services) == 0 {
		return nil
	}

	g := gen.NewGeneratedFile(file.GeneratedFilenamePrefix+"_client.pb.go", file.GoImportPath)

	// 同一 proto 文件中的服务共用由路径推导的服务名，规则与运行期服务注册共用 servicename。
	serviceName, err := servicename.FromPath(file.Desc.Path(), string(file.GoPackageName))
	if err != nil {
		return err
	}
	fd := &fileDesc{
		File:                     file,
		Path:                     file.Desc.Path(),
		ProtocGenGoClientRelease: Version,
		ProtocVersion:            protocVersion(gen),
		Deprecated:               file.Proto.GetOptions().GetDeprecated(),
		ServiceName:              serviceName,
		Symbols:                  buildGoSymbols(g),
	}

	for _, service := range file.Services {
		sd := &serviceDesc{
			Service:         service,
			ServiceName:     service.GoName,
			ServiceFullName: string(service.Desc.FullName()),
			ClientName:      serviceName,
			Deprecated:      serviceDeprecated(service),
		}
		for _, method := range service.Methods {
			if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
				return fmt.Errorf(
					"service %s method %s: streaming RPC is not supported",
					service.Desc.FullName(),
					method.Desc.Name(),
				)
			}
			httpRule, hasHTTPRule := proto.GetExtension(method.Desc.Options(), annotations.E_Http).(*annotations.HttpRule)
			md := &methodDesc{
				Method:      method,
				ServiceName: sd.ServiceName,
				Name:        method.GoName,
				Request:     g.QualifiedGoIdent(method.Input.GoIdent),
				Reply:       g.QualifiedGoIdent(method.Output.GoIdent),
				Comment:     methodComment(method),
				HasHTTPRule: hasHTTPRule && httpRule != nil,
			}
			sd.Methods = append(sd.Methods, md)
		}
		fd.Services = append(fd.Services, sd)
	}

	content, err := fd.execute()
	if err != nil {
		return fmt.Errorf("generate %s: %w", file.Desc.Path(), err)
	}
	g.P(content)
	return nil
}

// buildGoSymbols 通过 protogen 生成可能带别名的 Go 标识符，避免目标包名与 client、context 等依赖冲突。
func buildGoSymbols(g *protogen.GeneratedFile) goSymbols {
	return goSymbols{
		Context:         g.QualifiedGoIdent(contextPackage.Ident("Context")),
		Errorf:          g.QualifiedGoIdent(fmtPackage.Ident("Errorf")),
		WireNewSet:      g.QualifiedGoIdent(wirePackage.Ident("NewSet")),
		ClientFactory:   g.QualifiedGoIdent(clientPackage.Ident("Factory")),
		HTTPCallOptions: g.QualifiedGoIdent(clientPackage.Ident("HTTPCallOptionsFromContext")),
		GRPCCallOptions: g.QualifiedGoIdent(clientPackage.Ident("GRPCCallOptionsFromContext")),
	}
}

// serviceDeprecated 安全读取服务弃用标记，避免异常描述符类型导致生成器恐慌。
func serviceDeprecated(service *protogen.Service) bool {
	options, ok := service.Desc.Options().(*descriptorpb.ServiceOptions)
	return ok && options.GetDeprecated()
}

// methodComment 为生成方法提供稳定的中文头注释，同时保留 Proto 中的业务说明。
func methodComment(method *protogen.Method) string {
	comment := fmt.Sprintf("// %s 调用对应的 RPC 方法。", method.GoName)
	sourceComment := strings.TrimSpace(method.Comments.Leading.String() + method.Comments.Trailing.String())
	if sourceComment != "" {
		comment += "\n" + sourceComment
	}
	options, ok := method.Desc.Options().(*descriptorpb.MethodOptions)
	if ok && options.GetDeprecated() {
		comment += "\n// Deprecated: 请使用替代方法。"
	}
	return comment
}
