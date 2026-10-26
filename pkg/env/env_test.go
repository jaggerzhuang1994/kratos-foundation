package env

import (
	"os"
	"testing"
)

func TestAppEnvUsesPrimaryKeyAndClassifiesEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", Prod)
	t.Setenv("KRATOS_ENV", Dev)

	if got := AppEnv(); got != Prod {
		t.Fatalf("AppEnv() = %q, want %q", got, Prod)
	}
	if !IsProd() || !IsOnline() || IsOffline() || IsTest() {
		t.Fatalf("production environment classification is inconsistent")
	}
}

func TestAppEnvFallsBackAndRejectsInvalidValues(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("KRATOS_ENV", Test)
	defer func() {
		if recover() == nil {
			t.Fatal("AppEnv() did not reject an empty primary APP_ENV")
		}
	}()
	AppEnv()
}

func TestAppEnvUsesLegacyKeyAndDefaultsToLocal(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("KRATOS_ENV", "")
	if got := getEnv([]string{"APP_ENV", "KRATOS_ENV"}, Local); got != "" {
		t.Fatalf("getEnv() = %q, want present primary empty value", got)
	}

	t.Setenv("APP_ENV", "")
	t.Setenv("KRATOS_ENV", Test)
	if got := getEnv([]string{"KRATOS_ENV"}, Local); got != Test {
		t.Fatalf("legacy key = %q, want %q", got, Test)
	}
}

func TestGetEnvAndAppDebugDefaultsPriorityAndInvalidInput(t *testing.T) {
	t.Setenv("APP_DEBUG", "true")
	t.Setenv("KRATOS_DEBUG", "false")
	if !AppDebug() {
		t.Fatal("AppDebug() = false, want primary value true")
	}
	if got := GetEnv("MISSING_ENV", "fallback"); got != "fallback" {
		t.Fatalf("GetEnv() = %q, want fallback", got)
	}

	t.Setenv("APP_DEBUG", "not-a-bool")
	defer func() {
		if recover() == nil {
			t.Fatal("AppDebug() did not panic for invalid boolean")
		}
	}()
	AppDebug()
}

func TestIsLocalAndIsDevClassifyTheirExactEnvironments(t *testing.T) {
	t.Setenv("KRATOS_ENV", Prod)
	t.Setenv("APP_ENV", Local)
	if !IsLocal() || IsDev() {
		t.Fatalf("local classification: IsLocal=%t IsDev=%t", IsLocal(), IsDev())
	}

	t.Setenv("APP_ENV", Dev)
	if IsLocal() || !IsDev() {
		t.Fatalf("dev classification: IsLocal=%t IsDev=%t", IsLocal(), IsDev())
	}
}

func TestGetEnvAsBoolParsesExplicitValueAndUsesDefault(t *testing.T) {
	const explicitKey = "KRATOS_FOUNDATION_PUBLIC_BOOL"
	t.Setenv(explicitKey, "true")
	if !GetEnvAsBool(explicitKey, false) {
		t.Fatal("GetEnvAsBool() ignored explicit true value")
	}
	t.Setenv(explicitKey, "false")
	if GetEnvAsBool(explicitKey, true) {
		t.Fatal("GetEnvAsBool() ignored explicit false value")
	}

	const missingKey = "KRATOS_FOUNDATION_PUBLIC_BOOL_MISSING"
	previous, existed := os.LookupEnv(missingKey)
	if err := os.Unsetenv(missingKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(missingKey, previous)
		} else {
			_ = os.Unsetenv(missingKey)
		}
	})
	if !GetEnvAsBool(missingKey, true) {
		t.Fatal("GetEnvAsBool() did not use true default for absent variable")
	}
}

func TestGetEnvAsBoolRejectsInvalidExplicitValue(t *testing.T) {
	const key = "KRATOS_FOUNDATION_PUBLIC_BOOL_INVALID"
	t.Setenv(key, "not-a-bool")
	defer func() {
		if recover() == nil {
			t.Fatal("GetEnvAsBool() did not panic for invalid explicit value")
		}
	}()
	GetEnvAsBool(key)
}

func TestExposeDiagnostics(t *testing.T) {
	tests := []struct {
		name, env, debug string
		debugSet         bool
		want             bool
	}{
		{name: "default local", want: true},
		{name: "dev", env: Dev, want: true},
		{name: "test", env: Test, want: true},
		{name: "pre without debug", env: Pre, want: false},
		{name: "pre with debug", env: Pre, debug: "true", debugSet: true, want: true},
		{name: "pre with invalid debug", env: Pre, debug: "yes", debugSet: true, want: false},
		{name: "prod", env: Prod, want: false},
		{name: "prod ignores debug", env: Prod, debug: "true", debugSet: true, want: false},
		{name: "invalid env never panics", env: "PROD", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// 显式清空所有相关变量，避免宿主环境影响结果。
			for _, key := range append(append([]string(nil), appEnvKeys...), appDebugKeys...) {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			if test.env != "" {
				t.Setenv("APP_ENV", test.env)
			}
			if test.debugSet {
				t.Setenv("APP_DEBUG", test.debug)
			}
			if got := ExposeDiagnostics(); got != test.want {
				t.Fatalf("ExposeDiagnostics() = %v, want %v", got, test.want)
			}
		})
	}
}
