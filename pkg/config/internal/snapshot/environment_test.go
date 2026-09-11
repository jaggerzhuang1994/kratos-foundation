package snapshot

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/template"
)

func TestExpandEnvironment(t *testing.T) {
	t.Setenv("CONFIG_ENV_TEST", "value")
	t.Setenv("CONFIG_ENV_EMPTY", "")
	t.Setenv("CONFIG_ENV_LITERAL", "${CONFIG_ENV_TEST}")
	t.Setenv("CONFIG_ENV_MISSING", "")
	if err := os.Unsetenv("CONFIG_ENV_MISSING"); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ input, want string }{
		{"plain {ENV_VAR}", "plain {ENV_VAR}"},
		{"$CONFIG_ENV_TEST/${CONFIG_ENV_TEST}", "value/value"},
		{"${CONFIG_ENV_EMPTY}", ""},
		{"$CONFIG_ENV_MISSING/${CONFIG_ENV_MISSING}", "/"},
		{"${CONFIG_ENV_LITERAL}", "${CONFIG_ENV_TEST}"},
		{"${CONFIG_ENV_TEST:-fallback}", "value"},
		{"${CONFIG_ENV_EMPTY:-fallback}", "fallback"},
		{"${CONFIG_ENV_EMPTY-fallback}", ""},
		{"${CONFIG_ENV_MISSING-fallback}", "fallback"},
		{"${CONFIG_ENV_MISSING:-${CONFIG_ENV_TEST}}", "value"},
		{"${CONFIG_ENV_MISSING:-${CONFIG_ENV_EMPTY:-8080}}", "8080"},
		{"${CONFIG_ENV_TEST:?required}", "value"},
		{"${CONFIG_ENV_EMPTY?required}", ""},
		{"${CONFIG_ENV_TEST:+present}", "present"},
		{"${CONFIG_ENV_EMPTY:+present}", ""},
		{"${CONFIG_ENV_EMPTY+present}", "present"},
		{"$${CONFIG_ENV_TEST}/$$CONFIG_ENV_TEST", "${CONFIG_ENV_TEST}/$CONFIG_ENV_TEST"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			got, err := expandEnvironment(tt.input)
			if err != nil || got != tt.want {
				t.Fatalf("expand = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, input := range []string{"${CONFIG_ENV_MISSING:?required}", "${CONFIG_ENV_MISSING?required}", "${CONFIG_ENV_EMPTY:?required}"} {
		got, err := expandEnvironment(input)
		var required *template.MissingRequiredError
		if got != "" || !errors.As(err, &required) || required.Reason != "required" {
			t.Fatalf("required: %q, %v", got, err)
		}
	}
}

func TestExpandEnvironmentRejectsInvalidTemplatesWithoutLeakingContent(t *testing.T) {
	for _, input := range []string{
		"secret-content ${UNFINISHED",
		"secret-content ${VAR:fallback}",
		"secret-content ${}",
	} {
		got, err := expandEnvironment(input)
		if got != "" || err == nil || strings.Contains(err.Error(), "secret-content") {
			t.Fatalf("invalid template: %q, %v", got, err)
		}
	}
}
