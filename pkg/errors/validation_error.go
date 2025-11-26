package errors

import (
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
)

type ProtoValidationError interface {
	Field() string
	Reason() string
	Key() bool
	Cause() error
	ErrorName() string
}

type ValidationError struct {
	Field     string
	Reason    string
	Cause     error
	Key       bool
	ErrorName string
}

func NewValidationError(err ProtoValidationError) ValidationError {
	return ValidationError{
		Field:     err.Field(),
		Reason:    err.Reason(),
		Cause:     err.Cause(),
		Key:       err.Key(),
		ErrorName: err.ErrorName(),
	}
}

func (e ValidationError) MarshalJSON() ([]byte, error) {
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

func (e ValidationError) UnmarshalJSON(data []byte) error {
	v := struct {
		Field     string `json:"field"`
		Reason    string `json:"reason"`
		Cause     string `json:"cause"`
		Key       bool   `json:"key"`
		ErrorName string `json:"error_name"`
	}{}
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	e.Field = v.Field
	e.Reason = v.Reason
	if v.Cause != "" {
		e.Cause = errors.New(v.Cause)
	}
	e.Key = v.Key
	e.ErrorName = v.ErrorName
	return nil
}

// Error satisfies the builtin error interface
func (e ValidationError) Error() string {
	cause := ""
	if e.Cause != nil {
		cause = fmt.Sprintf(" | caused by: %v", e.Cause)
	}

	key := ""
	if e.Key {
		key = "key for "
	}

	return fmt.Sprintf(
		"invalid %sTest.%s: %s%s",
		key,
		e.Field,
		e.Reason,
		cause)
}

func ParseValidationError(validationErr error) (validationErrors []ValidationError) {
	var pbErrs []error

	if _, ok := validationErr.(ProtoValidationError); ok {
		pbErrs = []error{validationErr}
	} else if multiError, ok := validationErr.(interface {
		AllErrors() []error
	}); ok {
		pbErrs = multiError.AllErrors()
	}

	for _, err := range pbErrs {
		if validationErr, ok := err.(ProtoValidationError); ok {
			validationErrors = append(validationErrors, NewValidationError(validationErr))
		}
	}

	// 如果都不是校验错误，则包装一个未知错误
	if len(validationErrors) == 0 {
		validationErrors = []ValidationError{
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
