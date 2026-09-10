package oss

import (
	"context"
	"errors"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testconfig"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	foundationlog "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

func TestPublicBucketDomainHelperRoundTrip(t *testing.T) {
	helper, err := NewBucketDomainHelper(" https://cdn.example.com/assets/ ")
	if err != nil {
		t.Fatal(err)
	}
	fullURL, err := helper.GetFullURL("images/logo.png")
	if err != nil || fullURL != "https://cdn.example.com/assets/images/logo.png" {
		t.Fatalf("GetFullURL() = (%q, %v)", fullURL, err)
	}
	key, err := helper.ParseObjectKey(fullURL)
	if err != nil || key != "images/logo.png" {
		t.Fatalf("ParseObjectKey() = (%q, %v)", key, err)
	}
}

func managerStubOSSDriver(BucketConfig) (Bucket, error) {
	return nil, nil
}

type fakeBucket struct {
	closes   int
	closeErr error
}

func (*fakeBucket) PutObject(context.Context, string, io.Reader, PutOptions) (ObjectInfo, error) {
	return ObjectInfo{}, nil
}
func (*fakeBucket) GetObject(context.Context, string, GetOptions) (*Object, error) { return nil, nil }
func (*fakeBucket) DeleteObject(context.Context, string) error                     { return nil }
func (*fakeBucket) StatObject(context.Context, string) (ObjectInfo, error)         { return ObjectInfo{}, nil }
func (*fakeBucket) ObjectExists(context.Context, string) (bool, error)             { return false, nil }
func (b *fakeBucket) Close() error                                                 { b.closes++; return b.closeErr }

func TestManagerBucketCachesFactorySnapshotAndClose(t *testing.T) {
	original := map[string]string{"token": "before"}
	var received BucketConfig
	bucket := &fakeBucket{}
	m, err := newManager(&config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{"assets": {Driver: " FAKE ", Bucket: " physical ", Options: original}}}, map[string]DriverFactory{"fake": func(c BucketConfig) (Bucket, error) { received = c; c.Options["token"] = "mutated"; return bucket, nil }})
	if err != nil {
		t.Fatal(err)
	}
	original["token"] = "after"
	first, err := m.Bucket(" assets ")
	if err != nil || first != bucket {
		t.Fatalf("bucket=%v err=%v", first, err)
	}
	second, err := m.Bucket("assets")
	if err != nil || second != first {
		t.Fatalf("cached=%v err=%v", second, err)
	}
	if received.Bucket != "physical" || received.Options["token"] != "mutated" || m.definitions["assets"].config.Options["token"] != "before" {
		t.Fatalf("snapshot=%#v manager=%#v", received, m.definitions["assets"].config)
	}
	if got := strings.Join(m.BucketNames(), ","); got != "assets" {
		t.Fatalf("names=%s", got)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = m.close() })
	}
	wg.Wait()
	if bucket.closes != 1 {
		t.Fatalf("closes=%d", bucket.closes)
	}
	if _, err := m.Bucket("assets"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("error=%v", err)
	}
}

func TestManagerRejectsUnknownAndFactoryFailures(t *testing.T) {
	m, err := newManager(&config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{"assets": {Driver: "fake", Bucket: "x"}}}, map[string]DriverFactory{"fake": func(BucketConfig) (Bucket, error) { return nil, errors.New("open failed") }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Bucket("missing"); !errors.Is(err, ErrBucketUnknown) {
		t.Fatalf("error=%v", err)
	}
	if _, err := m.Bucket("assets"); err == nil || !strings.Contains(err.Error(), "open failed") {
		t.Fatalf("error=%v", err)
	}
}

func TestNewManagerRejectsUnregisteredDriver(t *testing.T) {
	_, err := newManager(&config_pb.OSS{
		Buckets: map[string]*config_pb.OSSBucket{
			"assets": {
				Driver: "qiniu",
				Bucket: "assets-production",
			},
		},
	}, map[string]DriverFactory{"aliyun": managerStubOSSDriver})
	if err == nil || !strings.Contains(err.Error(), "qiniu") {
		t.Fatalf("error = %v", err)
	}
}

type publicManagerLoadFailure struct {
	err      error
	key      string
	target   any
	defaults []any
}

func (m *publicManagerLoadFailure) Load(key string, target any, defaults ...any) error {
	m.key = key
	m.target = target
	m.defaults = defaults
	return m.err
}

func (*publicManagerLoadFailure) Subscribe(
	string,
	any,
	config.Observer,
	...any,
) (func(), error) {
	panic("NewManager must not subscribe to restart-only OSS configuration")
}

type publicManagerBucket struct {
	closes   int
	closeErr error
}

func (*publicManagerBucket) PutObject(context.Context, string, io.Reader, PutOptions) (ObjectInfo, error) {
	return ObjectInfo{}, nil
}

func (*publicManagerBucket) GetObject(context.Context, string, GetOptions) (*Object, error) {
	return nil, nil
}

func (*publicManagerBucket) DeleteObject(context.Context, string) error {
	return nil
}

func (*publicManagerBucket) StatObject(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{}, nil
}

func (*publicManagerBucket) ObjectExists(context.Context, string) (bool, error) {
	return false, nil
}

func (b *publicManagerBucket) Close() error {
	b.closes++
	return b.closeErr
}

func TestPublicNewManagerPreservesConfigLoadFailure(t *testing.T) {
	cause := errors.New("snapshot unavailable")
	configManager := &publicManagerLoadFailure{err: cause}

	manager, cleanup, err := NewManager(configManager, newPublicManagerLogger(t, ""))
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "load OSS config") {
		t.Fatalf("NewManager error = %v, want wrapped load failure", err)
	}
	if manager != nil || cleanup != nil {
		t.Fatalf("NewManager returned resources on load failure: manager=%t cleanup=%t", manager != nil, cleanup != nil)
	}
	if configManager.key != "oss" {
		t.Fatalf("loaded key = %q, want oss", configManager.key)
	}
	if _, ok := configManager.target.(*config_pb.OSS); !ok {
		t.Fatalf("load target type = %T, want *config_pb.OSS", configManager.target)
	}
	if len(configManager.defaults) != 1 {
		t.Fatalf("load defaults = %#v, want one OSS default", configManager.defaults)
	}
	if _, ok := configManager.defaults[0].(*config_pb.OSS); !ok {
		t.Fatalf("load default type = %T, want *config_pb.OSS", configManager.defaults[0])
	}
}

func TestPublicNewManagerAcceptsEmptyConfiguration(t *testing.T) {
	manager, cleanup, err := NewManager(testconfig.Empty(t), newPublicManagerLogger(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if manager == nil || cleanup == nil {
		t.Fatalf("NewManager returned manager=%t cleanup=%t", manager != nil, cleanup != nil)
	}
	if names := manager.BucketNames(); len(names) != 0 {
		t.Fatalf("empty config bucket names = %v", names)
	}
	cleanup()
	cleanup()
	if _, err := manager.Bucket("missing"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Bucket after cleanup error = %v, want ErrManagerClosed", err)
	}
}

func TestPublicNewManagerIsLazyAndCleanupIsIdempotent(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "oss.log")
	logger := newPublicManagerLogger(t, logPath).With("owner", "assets")
	component := &config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{
		"assets": {
			Driver:  " memory ",
			Bucket:  " asset-files ",
			Options: map[string]string{"region": "local"},
		},
	}}
	closeFailure := errors.New("close failed")
	bucket := &publicManagerBucket{closeErr: closeFailure}
	factoryCalls := 0
	var received BucketConfig
	drivers := map[string]DriverFactory{
		"memory": func(got BucketConfig) (Bucket, error) {
			factoryCalls++
			received = got
			return bucket, nil
		},
	}

	manager, cleanup, err := newManagerWithDrivers(testconfig.New(t, "oss", component), logger, drivers)
	if err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 0 {
		t.Fatalf("constructor opened a bucket %d times, want lazy construction", factoryCalls)
	}
	opened, err := manager.Bucket("assets")
	if err != nil || opened != bucket {
		t.Fatalf("Bucket = %v, err = %v", opened, err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factoryCalls)
	}
	if received.Name != "assets" || received.Bucket != "asset-files" || received.Options["region"] != "local" {
		t.Fatalf("factory config = %#v", received)
	}

	cleanup()
	cleanup()
	if bucket.closes != 1 {
		t.Fatalf("bucket closes = %d, want exactly 1 despite close error", bucket.closes)
	}
	written, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if line := string(written); !strings.Contains(line, "owner=assets") || !strings.Contains(line, "close failed") || strings.Count(line, "cleanup.failed") != 1 {
		t.Fatalf("cleanup must report once through its injected logger: %s", line)
	}
	if _, err := manager.Bucket("assets"); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Bucket after cleanup error = %v, want ErrManagerClosed", err)
	}
}

func newPublicManagerLogger(t testing.TB, path string) foundationlog.Logger {
	t.Helper()
	shared, cleanup, err := testlog.New(testlog.Config{
		Level: kratoslog.LevelInfo, TimeFormat: time.RFC3339,
		Std: testlog.OutputConfig{Disable: true, Level: kratoslog.LevelInfo},
		File: testlog.FileConfig{
			OutputConfig: testlog.OutputConfig{Disable: path == "", Level: kratoslog.LevelInfo},
			Path:         path, Rotating: testlog.RotatingConfig{Disable: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	return shared
}

func TestPublicNewManagerReturnsNoResourcesForInvalidConfiguration(t *testing.T) {
	component := &config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{
		"assets": {Driver: "missing", Bucket: "asset-files"},
	}}

	manager, cleanup, err := newManagerWithDrivers(testconfig.New(t, "oss", component), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unregistered driver") {
		t.Fatalf("NewManager error = %v, want unregistered-driver error", err)
	}
	if manager != nil || cleanup != nil {
		t.Fatalf("NewManager returned resources for invalid config: manager=%t cleanup=%t", manager != nil, cleanup != nil)
	}
}
