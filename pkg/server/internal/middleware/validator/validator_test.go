package validator

import (
	"context"
	stderrors "errors"
	"testing"

	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

type validatingRequest struct{ err error }

func (r validatingRequest) ValidateAll() error { return r.err }

func TestValidatorDisabledReturnsNil(t *testing.T) {
	disabled := true
	if got := Validator(&config_pb.Middleware_Validator{Disable: &disabled}); got != nil {
		t.Fatalf("Validator(disabled) = %v, want nil", got)
	}
}

func TestValidatorPassesRequestsWithoutValidationErrors(t *testing.T) {
	mw := Validator(nil)
	request := validatingRequest{}
	want := "response"
	got, err := mw(func(_ context.Context, received any) (any, error) {
		if received != request {
			t.Fatalf("handler request = %#v, want %#v", received, request)
		}
		return want, nil
	})(context.Background(), request)
	if err != nil || got != want {
		t.Fatalf("middleware result = (%v, %v), want (%q, nil)", got, err, want)
	}
}

func TestValidatorBlocksInvalidRequestAndPreservesCause(t *testing.T) {
	cause := stderrors.New("field is invalid")
	called := false
	_, err := Validator(nil)(func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})(context.Background(), validatingRequest{err: cause})
	if called {
		t.Fatal("handler ran for invalid request")
	}
	var foundationErr *foundationerrors.Error
	if !stderrors.As(err, &foundationErr) {
		t.Fatalf("error = %T %v, want foundation error", err, err)
	}
	if !stderrors.Is(err, cause) || foundationErr.Message != "request invalid" {
		t.Fatalf("validation error did not preserve cause and stable message: %v", err)
	}
}
