{{ range .Errors }}

{{ if .HasComment -}}
{{ .Comment }}
{{- end -}}
func Is{{.CamelValue}}(err error) bool {
	if err == nil {
		return false
	}
	e := errors.FromError(err)
	if e == nil {
		return false
	}
	return e.Code == {{ .HTTPCode }} && e.Reason == "{{ .Value }}" && e.Metadata != nil && e.Metadata["reason_code"] == "{{ .NumberValue }}"
}

{{ if .HasFormat -}}
{{ if .HasComment -}}
{{ .Comment }}
{{- end -}}
func Error{{ .CamelValue }}(format string, args ...any) *errors.Error {
	 return errors.New({{ .HTTPCode }}, "{{ .Value }}", fmt.Sprintf(format, args...)).WithReasonCode({{ .NumberValue }}).WithErrStack(4)
}

{{ if .HasComment -}}
{{ .Comment }}
{{- end -}}
func Error{{ .CamelValue }}WithFormat(args ...any) *errors.Error {
    var format = {{ .CommentLiteral }}
    return errors.New({{ .HTTPCode }}, "{{ .Value }}", fmt.Sprintf(format, args...)).WithReasonCode({{ .NumberValue }}).WithErrStack(4)
}
{{ else }}

{{ if .HasComment -}}
{{ .Comment }}
{{- end -}}
func Error{{ .CamelValue }}(formatAndArgs ...any) *errors.Error {
	var format string
	var args []any
	if len(formatAndArgs) > 0 {
		format = formatAndArgs[0].(string)
		args = formatAndArgs[1:]
{{ if .HasComment -}}
	} else { // 如果没有传参数，则默认填充注释为错误原因
		format = {{ .CommentLiteral }}
{{- end }}
	}
	 return errors.New({{ .HTTPCode }}, "{{ .Value }}", fmt.Sprintf(format, args...)).WithReasonCode({{ .NumberValue }}).WithErrStack(4)
}
{{- end }}

{{- end }}
