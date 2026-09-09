package app

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	foundationconfig "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

type stopPolicyConfigManager struct {
	mu           sync.Mutex
	initial      *config_pb.App
	observer     foundationconfig.Observer
	loadErr      error
	subscribeErr error
	canceled     bool
}

func (m *stopPolicyConfigManager) Load(key string, target any, defaults ...any) error {
	if m.loadErr != nil {
		return m.loadErr
	}
	if key != "app.stop_timeout" {
		return errors.New("unexpected load key")
	}
	proto.Merge(target.(*durationpb.Duration), m.initial.GetStopTimeout())
	return nil
}

func (m *stopPolicyConfigManager) Subscribe(
	key string,
	prototype any,
	observer foundationconfig.Observer,
	defaultValue ...any,
) (func(), error) {
	if m.subscribeErr != nil {
		return nil, m.subscribeErr
	}
	if key != "app.stop_timeout" {
		return nil, errors.New("unexpected subscription key")
	}
	if _, ok := prototype.(*durationpb.Duration); !ok || len(defaultValue) != 1 {
		return nil, errors.New("unexpected subscription contract")
	}
	m.mu.Lock()
	m.observer = observer
	initial := proto.CloneOf(m.initial)
	m.mu.Unlock()
	observer(key, initial.GetStopTimeout(), nil)
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.canceled = true
			m.observer = nil
			m.mu.Unlock()
		})
	}, nil
}

func (m *stopPolicyConfigManager) publish(value *config_pb.App, err error) {
	m.mu.Lock()
	observer := m.observer
	m.mu.Unlock()
	if observer != nil {
		observer("app.stop_timeout", proto.CloneOf(value.GetStopTimeout()), err)
	}
}

func validAppConfig(stopTimeout time.Duration) *config_pb.App {
	return &config_pb.App{
		RegistrarTimeout: durationpb.New(10 * time.Second),
		StopTimeout:      durationpb.New(stopTimeout),
	}
}

func TestNewStopPolicyAppliesValidUpdatesRejectsInvalidSnapshotsAndCancels(t *testing.T) {
	manager := &stopPolicyConfigManager{initial: validAppConfig(5 * time.Second)}
	logger := kratoslog.NewStdLogger(io.Discard)
	policy, cleanup, err := NewStopPolicy(
		validAppConfig(5*time.Second),
		manager,
		logger,
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.current(); got != 5*time.Second {
		t.Fatalf("initial stop timeout = %s, want 5s", got)
	}

	manager.publish(validAppConfig(7*time.Second), nil)
	if got := policy.current(); got != 7*time.Second {
		t.Fatalf("updated stop timeout = %s, want 7s", got)
	}

	invalidApp := validAppConfig(9 * time.Second)
	invalidApp.RegistrarTimeout = durationpb.New(0)
	manager.publish(invalidApp, nil)
	if got := policy.current(); got != 9*time.Second {
		t.Fatalf("unrelated app field prevented timeout update: %s", got)
	}
	manager.publish(validAppConfig(time.Second), nil)
	if got := policy.current(); got != 9*time.Second {
		t.Fatalf("timeout equal to stop delay changed policy to %s", got)
	}
	manager.publish(validAppConfig(11*time.Second), errors.New("watch failed"))
	if got := policy.current(); got != 9*time.Second {
		t.Fatalf("errored update changed policy to %s", got)
	}

	cleanup()
	cleanup()
	manager.publish(validAppConfig(13*time.Second), nil)
	if got := policy.current(); got != 9*time.Second {
		t.Fatalf("canceled subscription changed policy to %s", got)
	}
	manager.mu.Lock()
	canceled := manager.canceled
	manager.mu.Unlock()
	if !canceled {
		t.Fatal("cleanup did not cancel the app config subscription")
	}
}

func TestNewStopPolicyRejectsInitialPolicyAndSubscriptionFailure(t *testing.T) {
	logger := kratoslog.NewStdLogger(io.Discard)
	manager := &stopPolicyConfigManager{initial: validAppConfig(5 * time.Second)}
	if policy, cleanup, err := NewStopPolicy(
		validAppConfig(time.Second),
		manager,
		logger,
		time.Second,
	); err == nil || policy != nil || cleanup != nil {
		t.Fatalf("invalid initial timeout returned policy=%v cleanupPresent=%t err=%v", policy, cleanup != nil, err)
	}

	cause := errors.New("subscribe failed")
	manager.subscribeErr = cause
	policy, cleanup, err := NewStopPolicy(
		validAppConfig(5*time.Second),
		manager,
		logger,
		time.Second,
	)
	if !errors.Is(err, cause) || policy != nil || cleanup != nil {
		t.Fatalf("subscription failure returned policy=%v cleanupPresent=%t err=%v", policy, cleanup != nil, err)
	}
}

func TestValidateStopTimeoutAcceptsOnlyPositiveBudgetAboveDelay(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
		delay   time.Duration
		wantErr bool
	}{
		{name: "negative", timeout: -time.Second, wantErr: true},
		{name: "zero", timeout: 0, wantErr: true},
		{name: "equal delay", timeout: time.Second, delay: time.Second, wantErr: true},
		{name: "below delay", timeout: time.Second, delay: 2 * time.Second, wantErr: true},
		{name: "above delay", timeout: 2 * time.Second, delay: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStopTimeout(tt.timeout, tt.delay)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateStopTimeout(%s, %s) error = %v", tt.timeout, tt.delay, err)
			}
		})
	}
}

func TestStopPolicyConcurrentReadsAndUpdates(t *testing.T) {
	manager := &stopPolicyConfigManager{initial: validAppConfig(5 * time.Second)}
	policy, cleanup, err := NewStopPolicy(validAppConfig(5*time.Second), manager, kratoslog.NewStdLogger(io.Discard), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				manager.publish(validAppConfig(7*time.Second), nil)
				if got := policy.current(); got <= time.Second {
					t.Errorf("invalid budget: %s", got)
				}
			}
		}()
	}
	wg.Wait()
	manager.publish(validAppConfig(9*time.Second), nil)
	if got := policy.current(); got != 9*time.Second {
		t.Fatalf("latest budget = %s", got)
	}
}

func newStaticStopPolicy(timeout time.Duration) *StopPolicy {
	config := validAppConfig(timeout)
	policy, _, err := NewStopPolicy(config, &stopPolicyConfigManager{initial: config}, kratoslog.NewStdLogger(io.Discard), 0)
	if err != nil {
		panic(err)
	}
	return policy
}
