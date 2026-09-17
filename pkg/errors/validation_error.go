package errors

import (
	"encoding/json"
	"errors"
	"fmt"
)

// protoValidationError 只描述生成器错误的稳定方法集合，避免把具体生成实现暴露给业务。
type protoValidationError interface {
	error
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
}

// ValidationError 是单条 protobuf 校验失败的稳定传输表示。
type ValidationError struct {
	// Field 为校验失败的字段路径。
	Field string
	// Reason 描述违反的校验规则。
	Reason string
	// Cause 保存嵌套校验原因；JSON 传输仅保留其文本。
	Cause error
	// Key 为 true 时表示校验失败的是 map 键。
	Key bool
	// ErrorName 为生成器提供的校验错误名称。
	ErrorName string
}

// newValidationError 从生成器错误复制稳定字段，避免泄漏具体实现类型。
func newValidationError(err protoValidationError) *ValidationError {
	if err == nil {
		return nil
	}
	return &ValidationError{
		Field:     err.Field(),
		Reason:    err.Reason(),
		Cause:     err.Cause(),
		Key:       err.Key(),
		ErrorName: err.ErrorName(),
	}
}

// MarshalJSON 把底层原因序列化为文本，避免传输任意错误实现。
func (e *ValidationError) MarshalJSON() ([]byte, error) {
	var cause = ""
	if e.Cause != nil {
		cause = fmt.Sprintf("%+v", e.Cause)
	}
	return json.Marshal(map[string]any{
		"field":      e.Field,
		"reason":     e.Reason,
		"cause":      cause,
		"key":        e.Key,
		"error_name": e.ErrorName,
	})
}

// UnmarshalJSON 恢复校验失败，并把原因文本重建为普通错误。
func (e *ValidationError) UnmarshalJSON(data []byte) error {
	v := struct {
		// Field 为校验失败的字段路径。
		Field string `json:"field"`
		// Reason 描述违反的校验规则。
		Reason string `json:"reason"`
		// Cause 为原因文本；非空时还原为普通错误，不恢复原始错误类型。
		Cause string `json:"cause"`
		// Key 为 true 时表示校验失败的是 map 键。
		Key bool `json:"key"`
		// ErrorName 为生成器提供的校验错误名称。
		ErrorName string `json:"error_name"`
	}{}
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	e.Field = v.Field
	e.Reason = v.Reason
	e.Cause = nil
	if v.Cause != "" {
		e.Cause = errors.New(v.Cause)
	}
	e.Key = v.Key
	e.ErrorName = v.ErrorName
	return nil
}

// Error 返回稳定、可读的参数校验消息。
func (e *ValidationError) Error() string {
	cause := ""
	if e.Cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.Cause)
	}

	key := ""
	if e.Key {
		key = "key for "
	}

	target := e.Field
	if e.ErrorName != "" {
		target = e.ErrorName + "." + e.Field
	}

	return fmt.Sprintf("invalid %s%s: %s%s", key, target, e.Reason, cause)
}

// ParseValidationError 转换生成器的单个或聚合错误；其他错误保留为 unknown 记录。
func ParseValidationError(validationErr error) (validationErrors []*ValidationError) {
	if validationErr == nil {
		return nil
	}
	var pbErrs []error

	var protoErr protoValidationError
	if errors.As(validationErr, &protoErr) {
		pbErrs = []error{protoErr}
	} else {
		var multiError interface {
			AllErrors() []error
		}
		if errors.As(validationErr, &multiError) {
			pbErrs = multiError.AllErrors()
		}
	}

	for _, err := range pbErrs {
		var protoErr protoValidationError
		if errors.As(err, &protoErr) {
			validationErrors = append(validationErrors, newValidationError(protoErr))
		}
	}

	if len(validationErrors) == 0 {
		validationErrors = []*ValidationError{
			{
				Field:     "unknown",
				Reason:    "unknown",
				Cause:     validationErr,
				Key:       false,
				ErrorName: "unknown",
			},
		}
	}

	return
}
