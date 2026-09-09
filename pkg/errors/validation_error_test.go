package errors

import (
	stderrors "errors"
	"testing"
)

type fakeProtoValidationError struct {
	field string
	cause error
}

func (e fakeProtoValidationError) Error() string { return "invalid field" }

func (e fakeProtoValidationError) Field() string { return e.field }

func (e fakeProtoValidationError) Reason() string { return "required" }

func (e fakeProtoValidationError) Key() bool { return true }

func (e fakeProtoValidationError) Cause() error { return e.cause }

func (e fakeProtoValidationError) ErrorName() string { return "Fixture" }

type fakeMultiValidationError []error

func (e fakeMultiValidationError) Error() string { return "multiple validation errors" }

func (e fakeMultiValidationError) AllErrors() []error { return e }

func TestValidationErrorConversionAndJSONRoundTrip(t *testing.T) {
	cause := stderrors.New("nested")
	single := ParseValidationError(fakeProtoValidationError{field: "name", cause: cause})
	if len(single) != 1 || single[0].Field != "name" || single[0].Reason != "required" || !single[0].Key {
		t.Fatalf("single validation error = %#v", single)
	}
	if got := single[0].Error(); got != "invalid key for Fixture.name: required | caused by: nested" {
		t.Fatalf("ValidationError.Error() = %q", got)
	}

	multiple := ParseValidationError(fakeMultiValidationError{
		fakeProtoValidationError{field: "first"},
		stderrors.New("not generated"),
		fakeProtoValidationError{field: "second"},
	})
	if len(multiple) != 2 || multiple[0].Field != "first" || multiple[1].Field != "second" {
		t.Fatalf("multiple validation errors = %#v", multiple)
	}
	unknown := ParseValidationError(stderrors.New("unknown validation"))
	if len(unknown) != 1 || unknown[0].Field != "unknown" || unknown[0].Cause == nil {
		t.Fatalf("unknown validation error = %#v", unknown)
	}
	if ParseValidationError(nil) != nil {
		t.Fatal("ParseValidationError(nil) != nil")
	}

	encoded, err := single[0].MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	decoded := new(ValidationError)
	if err := decoded.UnmarshalJSON(encoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Field != "name" || decoded.Cause == nil || decoded.Cause.Error() != "nested" {
		t.Fatalf("validation JSON round trip = %#v", decoded)
	}
	if err := decoded.UnmarshalJSON([]byte("{")); err == nil {
		t.Fatal("UnmarshalJSON accepted malformed input")
	}
}
