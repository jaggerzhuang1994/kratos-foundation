package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// 全局导入的包
var importPackages = []string{
	"context",
	//"time",
	"github.com/google/wire",
	"github.com/pkg/errors",
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/component/client",
}

// generateFile .
func generateFile(gen *protogen.Plugin, file *protogen.File) *protogen.GeneratedFile {
	if len(file.Services) == 0 {
		return nil
	}

	g := gen.NewGeneratedFile(file.GeneratedFilenamePrefix+"_client.pb.go", file.GoImportPath)

	// 如果是根目录的文件，则用文件名作为 service_name
	serviceName := filepath.Dir(file.Desc.Path())
	if serviceName == "." {
		// 使用 package name
		serviceName = string(file.GoPackageName)
	}

	fd := &fileDesc{
		File:                     file,
		Path:                     file.Desc.Path(),
		ProtocGenGoClientRelease: Version,
		ProtocVersion:            protocVersion(gen),
		Deprecated:               file.Proto.GetOptions().GetDeprecated(),
		ServiceName:              serviceName,
	}

	for _, importPackage := range importPackages {
		g.QualifiedGoIdent(protogen.GoImportPath(importPackage).Ident("_"))
	}

	for _, service := range file.Services {
		sd := &serviceDesc{
			Service:         service,
			File:            fd,
			ServiceName:     service.GoName,
			ServiceFullName: string(service.Desc.FullName()),
			MethodSets:      map[string]*methodDesc{},
			HttpMethodSets:  map[string]*methodDesc{},
			Deprecated:      service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated(),
		}
		for _, method := range service.Methods {
			if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
				continue
			}
			comment := method.Comments.Leading.String() + method.Comments.Trailing.String()
			if comment != "" {
				comment = "// " + method.GoName + strings.TrimPrefix(strings.TrimSuffix(comment, "\n"), "//")
			} else {
				comment = "// " + method.GoName + " ."
			}
			if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
				comment += "\n// Deprecated: Do not use."
			}
			md := &methodDesc{
				Method:       method,
				ServiceName:  sd.ServiceName,
				Service:      sd,
				Name:         method.GoName,
				OriginalName: string(method.Desc.Name()),
				Request:      g.QualifiedGoIdent(method.Input.GoIdent),
				Reply:        g.QualifiedGoIdent(method.Output.GoIdent),
				Comment:      comment,
			}
			httpRule, ok := proto.GetExtension(method.Desc.Options(), annotations.E_Http).(*annotations.HttpRule)
			if httpRule != nil && ok {
				sd.HasHttp = true
				md.HttpRule = httpRule
				parseHttpMethod(service, md)
				sd.HttpMethodSets[md.Name] = md
			}

			sd.MethodSets[md.Name] = md
		}
		fd.Services = append(fd.Services, sd)
	}

	g.P(fd.execute())

	return g
}

func parseHttpMethod(service *protogen.Service, md *methodDesc) {
	if md.HttpRule == nil {
		return
	}
	httpRule := md.HttpRule
	var (
		path         string
		method       string
		body         string
		responseBody string
	)

	switch pattern := httpRule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		path = pattern.Get
		method = http.MethodGet
	case *annotations.HttpRule_Put:
		path = pattern.Put
		method = http.MethodPut
	case *annotations.HttpRule_Post:
		path = pattern.Post
		method = http.MethodPost
	case *annotations.HttpRule_Delete:
		path = pattern.Delete
		method = http.MethodDelete
	case *annotations.HttpRule_Patch:
		path = pattern.Patch
		method = http.MethodPatch
	case *annotations.HttpRule_Custom:
		path = pattern.Custom.Path
		method = pattern.Custom.Kind
	}
	if method == "" {
		method = http.MethodPost
	}
	if path == "" {
		path = fmt.Sprintf("%s/%s/%s", "", service.Desc.FullName(), md.Desc.Name())
	}
	body = httpRule.Body
	responseBody = httpRule.ResponseBody

	if method == http.MethodGet || method == http.MethodDelete {
		if body != "" {
			_, _ = fmt.Fprintf(os.Stderr, "\u001B[31mWARN\u001B[m: %s %s body should not be declared.\n", method, path)
		}
	} else {
		if body == "" {
			_, _ = fmt.Fprintf(os.Stderr, "\u001B[31mWARN\u001B[m: %s %s does not declare a body.\n", method, path)
		}
	}
	if body == "*" {
		md.HasBody = true
		md.Body = ""
	} else if body != "" {
		md.HasBody = true
		md.Body = "." + camelCaseVars(body)
	} else {
		md.HasBody = false
	}
	if responseBody == "*" {
		md.ResponseBody = ""
	} else if responseBody != "" {
		md.ResponseBody = "." + camelCaseVars(responseBody)
	}

	md.HttpMethod = method
	md.Path = path

	vars := buildPathVars(path)
	// check vars
	for v, s := range vars {
		fields := md.Method.Input.Desc.Fields()

		if s != nil {
			path = replacePath(v, *s, path)
		}
		for _, field := range strings.Split(v, ".") {
			if strings.TrimSpace(field) == "" {
				continue
			}
			if strings.Contains(field, ":") {
				field = strings.Split(field, ":")[0]
			}
			fd := fields.ByName(protoreflect.Name(field))
			if fd == nil {
				fmt.Fprintf(os.Stderr, "\u001B[31mERROR\u001B[m: The corresponding field '%s' declaration in message could not be found in '%s'\n", v, path)
				os.Exit(2)
			}
			if fd.IsMap() {
				fmt.Fprintf(os.Stderr, "\u001B[31mWARN\u001B[m: The field in path:'%s' shouldn't be a map.\n", v)
			} else if fd.IsList() {
				fmt.Fprintf(os.Stderr, "\u001B[31mWARN\u001B[m: The field in path:'%s' shouldn't be a list.\n", v)
			} else if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind {
				fields = fd.Message().Fields()
			}
		}
	}

	md.HasVars = len(vars) > 0
}

func buildPathVars(path string) (res map[string]*string) {
	if strings.HasSuffix(path, "/") {
		fmt.Fprintf(os.Stderr, "\u001B[31mWARN\u001B[m: Path %s should not end with \"/\" \n", path)
	}
	pattern := regexp.MustCompile(`(?i){([a-z.0-9_\s]*)=?([^{}]*)}`)
	matches := pattern.FindAllStringSubmatch(path, -1)
	res = make(map[string]*string, len(matches))
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if len(name) > 1 && len(m[2]) > 0 {
			res[name] = &m[2]
		} else {
			res[name] = nil
		}
	}
	return
}
