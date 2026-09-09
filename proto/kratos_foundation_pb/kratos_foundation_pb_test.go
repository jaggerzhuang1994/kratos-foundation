package kratos_foundation_pb

import (
	"fmt"
	"testing"

	foundationerrors "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/errors"
)

func TestGeneratedErrorPreservesLegacyFormatting(t *testing.T) {
	for _, test := range []struct {
		args []any
		want string
	}{
		{[]any{"user %s not found", "alice"}, "user alice not found"},
		{[]any{"100% complete"}, "100% complete"},
		{[]any{42, " failed"}, "42 failed"},
	} {
		if got := ErrorValidator(test.args...).Message; got != test.want {
			t.Errorf("ErrorValidator(%v) = %q, want %q", test.args, got, test.want)
		}
	}
	if got := ErrorValidator(fmt.Sprintf("user %s", "alice")).Message; got != "user alice" {
		t.Fatal(got)
	}
}

func TestIsValidatorMatchesStableIdentity(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "different HTTP code",
			err:  foundationerrors.New(500, "VALIDATOR", "failed").WithReasonCode(422),
			want: true,
		},
		{
			name: "gRPC round trip",
			err:  ErrorValidator().GRPCStatus().Err(),
			want: true,
		},
		{
			name: "different reason",
			err:  foundationerrors.New(422, "OTHER", "failed").WithReasonCode(422),
		},
		{
			name: "different reason code",
			err:  foundationerrors.New(422, "VALIDATOR", "failed").WithReasonCode(423),
		},
		{
			name: "missing reason code",
			err:  foundationerrors.New(422, "VALIDATOR", "failed"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsValidator(test.err); got != test.want {
				t.Fatalf("IsValidator() = %v, want %v", got, test.want)
			}
		})
	}
}
