package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"
)

//go:embed errorsTemplate.tpl
var errorsTemplate string

// errorInfo 是单个 Proto 错误枚举值渲染模板所需的最小数据。
type errorInfo struct {
	// Value 为 Proto 枚举值原名，用作错误原因标识。
	Value string
	// HTTPCode 为生成错误的 HTTP 状态码。
	HTTPCode int
	// CamelValue 为生成辅助函数使用的驼峰名称。
	CamelValue string
	// Comment 是可直接写入 Go 源码的业务注释。
	Comment string
	// CommentLiteral 是已安全转义的 Go 字符串字面量。
	CommentLiteral string
	// HasComment 表示是否有可用的默认消息文本。
	HasComment bool
	// NumberValue 是用于 reason_code 元数据的 Proto 枚举数值。
	NumberValue int32
	// HasFormat 表示默认消息包含 fmt 格式化指令。
	HasFormat bool
}

// errorWrapper 聚合一个 Proto 枚举中需要生成的错误值。
type errorWrapper struct {
	// Errors 保存本枚举待生成的错误定义。
	Errors []*errorInfo
	// Symbols 保存已解析导入别名的 Go 标识符。
	Symbols errorGoSymbols
	// ErrorStackSkip 为生成函数记录错误栈时跳过的调用层数。
	ErrorStackSkip int
}

// errorGoSymbols 保存带正确导入别名的 Go 标识符。
type errorGoSymbols struct {
	// ErrorType 为业务错误类型的限定标识符。
	ErrorType string
	// FromError 为错误转换函数的限定标识符。
	FromError string
	// New 为错误构造函数的限定标识符。
	New string
	// Sprintf 为格式化消息函数的限定标识符。
	Sprintf string
	// Sprint 为拼接消息函数的限定标识符。
	Sprint string
}

// execute 渲染错误辅助函数，并将模板错误交给 protoc 报告。
func (e *errorWrapper) execute() (string, error) {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("errors").Parse(errorsTemplate)
	if err != nil {
		return "", fmt.Errorf("parse errors template: %w", err)
	}
	if err := tmpl.Execute(buf, e); err != nil {
		return "", fmt.Errorf("execute errors template: %w", err)
	}
	return buf.String(), nil
}
