{{$symbols := .Symbols}}
{{$errorStackSkip := .ErrorStackSkip}}
{{ range .Errors }}

// Is{{.CamelValue}} 判断 err 是否为 {{.Value}} 错误。
{{if .HasComment}}{{.Comment}}
{{end}}func Is{{.CamelValue}}(err error) bool {
	if err == nil {
		return false
	}
	e := {{$symbols.FromError}}(err)
	if e == nil {
		return false
	}
	return e.Reason == "{{ .Value }}" && e.Metadata != nil && e.Metadata["reason_code"] == "{{ .NumberValue }}"
}

// Error{{.CamelValue}} 创建 {{.Value}} 错误；无参数使用 Proto 注释，单参数保留原文。
// 为兼容旧调用，字符串首参数带后续参数时按 fmt.Sprintf 格式化；新代码可先显式格式化消息。
{{if .HasComment}}{{.Comment}}
{{end}}func Error{{ .CamelValue }}(messageParts ...any) *{{$symbols.ErrorType}} {
	message := {{ .CommentLiteral }}
	if len(messageParts) > 0 {
		message = {{$symbols.Sprint}}(messageParts...)
		if format, ok := messageParts[0].(string); ok && len(messageParts) > 1 {
			message = {{$symbols.Sprintf}}(format, messageParts[1:]...)
		}
	}
	return {{$symbols.New}}({{ .HTTPCode }}, "{{ .Value }}", message).WithReasonCode({{ .NumberValue }}).WithErrStack({{$errorStackSkip}})
}

{{ if .HasFormat -}}
// Error{{ .CamelValue }}WithFormat 使用 Proto 注释作为格式模板创建 {{.Value}} 错误。
func Error{{ .CamelValue }}WithFormat(args ...any) *{{$symbols.ErrorType}} {
	return {{$symbols.New}}({{ .HTTPCode }}, "{{ .Value }}", {{$symbols.Sprintf}}({{ .CommentLiteral }}, args...)).WithReasonCode({{ .NumberValue }}).WithErrStack({{$errorStackSkip}})
}
{{- end }}

{{- end }}
