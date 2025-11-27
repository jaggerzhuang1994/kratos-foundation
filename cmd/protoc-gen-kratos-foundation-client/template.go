package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
)

//go:embed template.tpl
var clientTemplate string

type fileDesc struct {
	*protogen.File
	// 相对import路径的path
	Path string
	// ProtocGenGoClientRelease 版本
	ProtocGenGoClientRelease string
	// Protoc 版本
	ProtocVersion string
	// 文件是否弃用
	Deprecated bool
	// 服务注册名称，为 proto 文件目录名
	ServiceName string
	// 声明的 service
	Services []*serviceDesc
}

type serviceDesc struct {
	*protogen.Service
	File            *fileDesc
	ServiceName     string // Greeter
	ServiceFullName string // helloworld.Greeter
	MethodSets      map[string]*methodDesc
	HttpMethodSets  map[string]*methodDesc
	Deprecated      bool
	HasHttp         bool
}

type methodDesc struct {
	*protogen.Method
	ServiceName string
	Service     *serviceDesc
	// method
	Name         string
	OriginalName string // The parsed original name
	Request      string
	Reply        string
	Comment      string
	// http_rule
	*annotations.HttpRule
	Path         string
	HttpMethod   string
	HasVars      bool
	HasBody      bool
	Body         string
	ResponseBody string
}

var funcs = map[string]any{
	"lcfirst": lcfirst,
}

func (fd *fileDesc) execute() string {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("client").Funcs(funcs).Parse(strings.TrimSpace(clientTemplate))
	if err != nil {
		panic(err)
	}

	if err := tmpl.Execute(buf, fd); err != nil {
		panic(err)
	}
	return strings.Trim(buf.String(), "\r\n")
}

func lcfirst(s string) string { return strings.ToLower(s[:1]) + s[1:] }
