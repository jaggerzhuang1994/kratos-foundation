package log

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
)

var logEnvironmentKeys = []string{
	EnvLevel,
	EnvFilterEmpty,
	EnvFilterKeys,
	EnvTimeFormat,
	EnvStdDisable,
	EnvStdLevel,
	EnvStdFilterKeys,
	EnvFileDisable,
	EnvFileLevel,
	EnvFileFilterKeys,
	EnvFilePath,
	EnvFileRotatingDisable,
	EnvFileRotatingMaxSize,
	EnvFileRotatingMaxFileAge,
	EnvFileRotatingMaxFiles,
	EnvFileRotatingLocalTime,
	EnvFileRotatingCompress,
}

func TestEnvStringUsesFallbackOnlyWhenVariableIsAbsent(t *testing.T) {
	const key = "KRATOS_FOUNDATION_TEST_LOG_STRING"
	clearEnvironment(t, key)

	if got := envString(key, "fallback"); got != "fallback" {
		t.Fatalf("unset value = %q, want fallback", got)
	}
	t.Setenv(key, "")
	if got := envString(key, "fallback"); got != "" {
		t.Fatalf("explicit empty value = %q, want empty", got)
	}
	t.Setenv(key, " raw value ")
	if got := envString(key, "fallback"); got != " raw value " {
		t.Fatalf("raw value = %q, want whitespace preserved", got)
	}
}

func TestEnvBoolParsesTrimmedValuesAndRejectsInvalidInput(t *testing.T) {
	const key = "KRATOS_FOUNDATION_TEST_LOG_BOOL"
	clearEnvironment(t, key)

	got, err := envBool(key, true)
	if err != nil || !got {
		t.Fatalf("unset value = %v, err = %v, want fallback true", got, err)
	}
	t.Setenv(key, " FALSE ")
	got, err = envBool(key, true)
	if err != nil || got {
		t.Fatalf("parsed value = %v, err = %v, want false", got, err)
	}
	t.Setenv(key, "not-a-bool")
	if _, err = envBool(key, false); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("invalid value error = %v, want error naming %s", err, key)
	}
}

func TestEnvNonNegativeIntHonorsFallbackAndBounds(t *testing.T) {
	const key = "KRATOS_FOUNDATION_TEST_LOG_INT"
	clearEnvironment(t, key)

	got, err := envNonNegativeInt(key, 7)
	if err != nil || got != 7 {
		t.Fatalf("unset value = %d, err = %v, want fallback 7", got, err)
	}
	for _, test := range []struct {
		name  string
		value string
		want  int
	}{
		{name: "zero", value: " 0 ", want: 0},
		{name: "positive", value: "12", want: 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(key, test.value)
			got, err := envNonNegativeInt(key, 99)
			if err != nil || got != test.want {
				t.Fatalf("value = %d, err = %v, want %d", got, err, test.want)
			}
		})
	}
	for _, value := range []string{"-1", "not-an-int"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := envNonNegativeInt(key, 0); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("value %q error = %v, want error naming %s", value, err, key)
			}
		})
	}
}

func TestEnvLevelHonorsFallbackAndParsesEverySupportedLevel(t *testing.T) {
	const key = "KRATOS_FOUNDATION_TEST_LOG_LEVEL"
	clearEnvironment(t, key)

	got, err := envLevel(key, kratoslog.LevelWarn)
	if err != nil || got != kratoslog.LevelWarn {
		t.Fatalf("unset level = %v, err = %v, want warn", got, err)
	}
	for _, test := range []struct {
		value string
		want  kratoslog.Level
	}{
		{value: " DEBUG ", want: kratoslog.LevelDebug},
		{value: "Info", want: kratoslog.LevelInfo},
		{value: "warn", want: kratoslog.LevelWarn},
		{value: "ERROR", want: kratoslog.LevelError},
		{value: "fatal", want: kratoslog.LevelFatal},
	} {
		t.Run(strings.TrimSpace(test.value), func(t *testing.T) {
			t.Setenv(key, test.value)
			got, err := envLevel(key, kratoslog.LevelDebug)
			if err != nil || got != test.want {
				t.Fatalf("level = %v, err = %v, want %v", got, err, test.want)
			}
		})
	}
	t.Setenv(key, "trace")
	if _, err := envLevel(key, kratoslog.LevelInfo); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("invalid level error = %v, want error naming %s", err, key)
	}
}

func TestEnvCSVCopiesFallbackAndNormalizesExplicitList(t *testing.T) {
	const key = "KRATOS_FOUNDATION_TEST_LOG_CSV"
	clearEnvironment(t, key)

	fallback := []string{"service.id", "service.name"}
	got := envCSV(key, fallback)
	if !slices.Equal(got, fallback) {
		t.Fatalf("unset list = %v, want %v", got, fallback)
	}
	got[0] = "mutated"
	if fallback[0] != "service.id" {
		t.Fatalf("fallback was aliased: %v", fallback)
	}

	t.Setenv(key, " token, ,password,token, request.id ")
	if got := envCSV(key, fallback); !slices.Equal(got, []string{"token", "password", "request.id"}) {
		t.Fatalf("normalized list = %v", got)
	}
	t.Setenv(key, " , ")
	if got := envCSV(key, fallback); len(got) != 0 || got == nil {
		t.Fatalf("explicit empty list = %#v, want non-nil empty slice", got)
	}
}

func TestNewConfigAppliesDocumentedDefaults(t *testing.T) {
	clearEnvironment(t, logEnvironmentKeys...)

	got, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Level:       kratoslog.LevelInfo,
		FilterEmpty: true,
		TimeFormat:  time.RFC3339,
		Std: OutputConfig{
			Level:      kratoslog.LevelInfo,
			FilterKeys: []string{"service.id", "service.name", "service.version"},
		},
		File: FileConfig{
			OutputConfig: OutputConfig{Level: kratoslog.LevelInfo},
			Path:         "./app.log",
			Rotating: RotatingConfig{
				MaxSize: 100,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default config = %#v, want %#v", got, want)
	}
}

func TestNewConfigLoadsEveryEnvironmentVariable(t *testing.T) {
	clearEnvironment(t, logEnvironmentKeys...)
	values := map[string]string{
		EnvLevel:                  "warn",
		EnvFilterEmpty:            "false",
		EnvFilterKeys:             "token,password,token",
		EnvTimeFormat:             "2006",
		EnvStdDisable:             "true",
		EnvStdLevel:               "error",
		EnvStdFilterKeys:          "request.id",
		EnvFileDisable:            "true",
		EnvFileLevel:              "fatal",
		EnvFileFilterKeys:         "secret",
		EnvFilePath:               "",
		EnvFileRotatingDisable:    "true",
		EnvFileRotatingMaxSize:    "9",
		EnvFileRotatingMaxFileAge: "4",
		EnvFileRotatingMaxFiles:   "2",
		EnvFileRotatingLocalTime:  "true",
		EnvFileRotatingCompress:   "true",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}

	got, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Level:       kratoslog.LevelWarn,
		FilterEmpty: false,
		FilterKeys:  []string{"token", "password"},
		TimeFormat:  "2006",
		Std: OutputConfig{
			Disable:    true,
			Level:      kratoslog.LevelError,
			FilterKeys: []string{"request.id"},
		},
		File: FileConfig{
			OutputConfig: OutputConfig{
				Disable:    true,
				Level:      kratoslog.LevelFatal,
				FilterKeys: []string{"secret"},
			},
			Path: "",
			Rotating: RotatingConfig{
				Disable:    true,
				MaxSize:    9,
				MaxFileAge: 4,
				MaxFiles:   2,
				LocalTime:  true,
				Compress:   true,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured value = %#v, want %#v", got, want)
	}
}

func TestNewConfigRejectsMalformedAndOutOfRangeEnvironment(t *testing.T) {
	for _, test := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "root level", key: EnvLevel, value: "trace"},
		{name: "filter empty", key: EnvFilterEmpty, value: "sometimes"},
		{name: "standard disable", key: EnvStdDisable, value: "sometimes"},
		{name: "standard level", key: EnvStdLevel, value: "trace"},
		{name: "file disable", key: EnvFileDisable, value: "sometimes"},
		{name: "file level", key: EnvFileLevel, value: "trace"},
		{name: "rotating disable", key: EnvFileRotatingDisable, value: "sometimes"},
		{name: "max size syntax", key: EnvFileRotatingMaxSize, value: "large"},
		{name: "max size negative", key: EnvFileRotatingMaxSize, value: "-1"},
		{name: "max size zero", key: EnvFileRotatingMaxSize, value: "0"},
		{name: "max file age syntax", key: EnvFileRotatingMaxFileAge, value: "old"},
		{name: "max file age negative", key: EnvFileRotatingMaxFileAge, value: "-1"},
		{name: "max files syntax", key: EnvFileRotatingMaxFiles, value: "many"},
		{name: "max files negative", key: EnvFileRotatingMaxFiles, value: "-1"},
		{name: "local time", key: EnvFileRotatingLocalTime, value: "sometimes"},
		{name: "compress", key: EnvFileRotatingCompress, value: "sometimes"},
		{name: "empty time format", key: EnvTimeFormat, value: " \t "},
		{name: "empty enabled file path", key: EnvFilePath, value: " \t "},
	} {
		t.Run(test.name, func(t *testing.T) {
			clearEnvironment(t, logEnvironmentKeys...)
			t.Setenv(test.key, test.value)
			_, err := NewConfig()
			if err == nil || !strings.Contains(err.Error(), test.key) {
				t.Fatalf("NewConfig error = %v, want error naming %s", err, test.key)
			}
		})
	}
}

func clearEnvironment(t testing.TB, keys ...string) {
	t.Helper()
	for _, key := range keys {
		value, present := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if present {
				if err := os.Setenv(key, value); err != nil {
					t.Errorf("restore %s: %v", key, err)
				}
				return
			}
			if err := os.Unsetenv(key); err != nil {
				t.Errorf("clear %s: %v", key, err)
			}
		})
	}
}

// These cases kill mutations that change documented environment defaults or
// silently accept malformed values before constructing a shared logger.
func TestNewConfigDefaultsAndEnvironment(t *testing.T) {
	config, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Level != kratoslog.LevelInfo || !config.FilterEmpty || config.Std.Disable || config.File.Disable {
		t.Fatalf("defaults = %#v", config)
	}
	t.Setenv(EnvLevel, "debug")
	t.Setenv(EnvStdDisable, "true")
	t.Setenv(EnvFileDisable, "true")
	t.Setenv(EnvFilterKeys, "token,password")
	config, err = NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Level != kratoslog.LevelDebug || !config.Std.Disable || !config.File.Disable || len(config.FilterKeys) != 2 {
		t.Fatalf("environment config = %#v", config)
	}
}

func TestNewConfigRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv(EnvLevel, "verbose")
	if _, err := NewConfig(); err == nil {
		t.Fatal("invalid level accepted")
	}
	t.Setenv(EnvLevel, "info")
	t.Setenv(EnvFileRotatingMaxSize, "0")
	if _, err := NewConfig(); err == nil {
		t.Fatal("zero rotating max size accepted")
	}
}
