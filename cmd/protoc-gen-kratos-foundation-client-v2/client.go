package main

import (
	"fmt"
	"path"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

var (
	contextPackage = protogen.GoImportPath("context")
	fmtPackage     = protogen.GoImportPath("fmt")
	wirePackage    = protogen.GoImportPath("github.com/google/wire")
	clientPackage  = protogen.GoImportPath("github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/client")
)

// clientNameOptionFullName 是 client_name 服务选项的 Proto 全名。
const clientNameOptionFullName protoreflect.FullName = "kratos_foundation_client.client_name"

// generateFile 为含有服务的 Proto 文件生成 Factory 客户端适配层。
func generateFile(gen *protogen.Plugin, file *protogen.File) error {
	if len(file.Services) == 0 {
		return nil
	}

	g := gen.NewGeneratedFile(file.GeneratedFilenamePrefix+"_client.pb.go", file.GoImportPath)

	fd := &fileDesc{
		File:                     file,
		Path:                     file.Desc.Path(),
		ProtocGenGoClientRelease: Version,
		ProtocVersion:            protocVersion(gen),
		Deprecated:               file.Proto.GetOptions().GetDeprecated(),
		ServiceName:              defaultServiceName(file.Desc.Path(), file.GoPackageName),
		Symbols:                  buildGoSymbols(g),
	}

	for _, service := range file.Services {
		clientName, err := serviceClientName(file, service, fd.ServiceName)
		if err != nil {
			return err
		}
		sd := &serviceDesc{
			Service:         service,
			ServiceName:     service.GoName,
			ServiceFullName: string(service.Desc.FullName()),
			ClientName:      clientName,
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

// defaultServiceName 按 Proto 目录、文件名、Go 包名的顺序推导默认连接名。
func defaultServiceName(protoPath string, goPackageName protogen.GoPackageName) string {
	serviceName := path.Base(path.Dir(protoPath))
	if serviceName == "." {
		serviceName = strings.TrimSuffix(path.Base(protoPath), path.Ext(protoPath))
	}
	if serviceName == "" || serviceName == "." || serviceName == "/" {
		return string(goPackageName)
	}
	return serviceName
}

// serviceClientName 读取服务级 client_name；这里必须读 FileDescriptorProto，因为 protogen 构建反射描述符后才会重新解析未知扩展。
func serviceClientName(file *protogen.File, service *protogen.Service, fallback string) (string, error) {
	index := service.Desc.Index()
	services := file.Proto.GetService()
	if index < 0 || index >= len(services) {
		return "", fmt.Errorf("service %s: descriptor index %d is out of range", service.Desc.FullName(), index)
	}

	options := services[index].GetOptions()
	if options == nil {
		return fallback, nil
	}

	clientName := fallback
	found := false
	var optionErr error
	proto.RangeExtensions(options, func(extension protoreflect.ExtensionType, value any) bool {
		if extension.TypeDescriptor().FullName() != clientNameOptionFullName {
			return true
		}

		configuredName, ok := value.(string)
		if !ok {
			optionErr = fmt.Errorf(
				"service %s: option %s has unexpected type %T",
				service.Desc.FullName(),
				clientNameOptionFullName,
				value,
			)
			return false
		}

		clientName = configuredName
		found = true
		return false
	})
	if optionErr != nil {
		return "", optionErr
	}
	if found && strings.TrimSpace(clientName) == "" {
		return "", fmt.Errorf(
			"service %s: option %s must not be blank",
			service.Desc.FullName(),
			clientNameOptionFullName,
		)
	}
	if found && clientName != strings.TrimSpace(clientName) {
		return "", fmt.Errorf(
			"service %s: option %s must not have leading or trailing whitespace",
			service.Desc.FullName(),
			clientNameOptionFullName,
		)
	}
	return clientName, nil
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
