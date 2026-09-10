package bootstrap_test

import (
	"context"
	"errors"

	"reflect"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/app"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/job"
)

func TestUnifiedSpecSharesHooksAndFreeze(t *testing.T) {
	spec := bootstrap.NewSpec()
	application := bootstrap.ApplicationSpec(spec)
	if application != bootstrap.ApplicationSpec(spec) {
		t.Fatal("application Spec was recreated")
	}
	started := false
	if err := spec.BeforeStart(func(context.Context) error { started = true; return nil }); err != nil {
		t.Fatal(err)
	}
	spec.Job().RegisterOnce("finish", job.TaskFunc(func(context.Context) error { return nil })).ExitWhenDone()
	logger, tracing, metrics := newTestObservability(t)
	_, cleanup, err := bootstrap.NewComponentsBootstrap(spec, nil, logger, metrics, tracing, bootstrap.Bootstrap{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := runComponentsApp(t, application); err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("hook registered through bootstrap.Spec did not run")
	}
	for name, register := range map[string]func() error{
		"context":      func() error { return spec.AddContext(func(ctx context.Context) context.Context { return ctx }) },
		"metadata":     func() error { return spec.AddMetadata(map[string]string{"late": "value"}) },
		"endpoints":    func() error { return spec.AddEndpoints() },
		"signals":      func() error { return spec.AddSignals() },
		"before start": func() error { return spec.BeforeStart() },
		"after start":  func() error { return spec.AfterStart() },
		"before stop":  func() error { return spec.BeforeStop() },
		"after stop":   func() error { return spec.AfterStop() },
	} {
		if err := register(); !errors.Is(err, app.ErrSpecFrozen) {
			t.Errorf("%s did not share application freeze: %v", name, err)
		}
	}
}

// 防止匿名嵌入重新将内部生命周期装配能力暴露给业务。
func TestSpecHidesInfrastructureAssembly(t *testing.T) {
	typ := reflect.TypeOf((*bootstrap.Spec)(nil))
	for _, name := range []string{"RegisterAppInfo", "RegisterLogger", "Runtime", "Ready", "App", "Log"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("internal method %s is exposed", name)
		}
	}
	fields := typ.Elem()
	for i := 0; i < fields.NumField(); i++ {
		if fields.Field(i).IsExported() {
			t.Errorf("assembly field %s is exposed", fields.Field(i).Name)
		}
	}
}
