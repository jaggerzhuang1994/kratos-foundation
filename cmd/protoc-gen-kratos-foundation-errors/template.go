package main

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed errorsTemplate.tpl
var errorsTemplate string

type errorInfo struct {
	Name           string // 枚举名字
	Value          string // 错误码枚举key
	HTTPCode       int    // http 状态码
	CamelValue     string // 错误码枚举key转成大写驼峰
	Comment        string // 备注
	CommentLiteral string // 备注字面量（转成字符串包裹类型）
	HasComment     bool   // 是否包含备注
	NumberValue    int32  // 错误码枚举值
	HasFormat      bool   // 备注中是否包含 % 占位符， 不包含%% 和 %空格
}

type errorWrapper struct {
	Errors []*errorInfo
}

func (e *errorWrapper) execute() string {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("errors").Parse(errorsTemplate)
	if err != nil {
		panic(err)
	}
	if err := tmpl.Execute(buf, e); err != nil {
		panic(err)
	}
	return buf.String()
}
