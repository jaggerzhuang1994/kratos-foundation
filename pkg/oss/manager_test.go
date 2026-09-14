package oss

import (
	"context"
	"errors"
	"fmt"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/testlog"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	if line := string(written); !strings.Contains(line, "owner=assets") || !strings.Contains(line, "close failed") || strings.Count(line, "Failed to close an object storage client") != 1 {
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

// BenchmarkManagerCachedDuringCreation 注入 1ms factory 延迟，测量其对另一缓存命中的阻塞。
// 时间只包含缓存命中；每轮等待创建结束，避免跨轮后台工作干扰结果。
func BenchmarkManagerCachedDuringCreation(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan struct{})
		bucket := &fakeBucket{}
		m, err := newManager(&config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{
			"cached": {Driver: "fake", Bucket: "cached"}, "slow": {Driver: "fake", Bucket: "slow"},
		}}, map[string]DriverFactory{"fake": func(c BucketConfig) (Bucket, error) {
			if c.Name == "slow" {
				close(entered)
				<-release
			}
			return bucket, nil
		}})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := m.Bucket("cached"); err != nil {
			b.Fatal(err)
		}
		go func() {
			_, err := m.Bucket("slow")
			if err != nil {
				b.Error(err)
			}
			close(finished)
		}()
		<-entered
		timer := time.AfterFunc(time.Millisecond, func() { close(release) })
		b.StartTimer()
		if _, err := m.Bucket("cached"); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		<-finished
		timer.Stop()
		if err := m.close(); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}

func TestManagerCreationDoesNotBlockOtherBucketsAndCloseWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		buckets := map[string]*fakeBucket{"cached": {}, "slow": {}, "other": {}}
		m, err := newManager(&config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{
			"cached": {Driver: "fake", Bucket: "cached"}, "slow": {Driver: "fake", Bucket: "slow"}, "other": {Driver: "fake", Bucket: "other"},
		}}, map[string]DriverFactory{"fake": func(c BucketConfig) (Bucket, error) {
			if c.Name == "slow" {
				close(entered)
				<-release
			}
			return buckets[c.Name], nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Bucket("cached"); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 3)
		go func() { _, err := m.Bucket("slow"); done <- err }()
		<-entered
		for _, name := range []string{"cached", "other"} {
			go func() { _, err := m.Bucket(name); done <- err }()
		}
		synctest.Wait()
		if len(done) != 2 {
			close(release)
			t.Fatal("slow factory blocked unrelated buckets")
		}
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		closed := make(chan error, 1)
		go func() { closed <- m.close() }()
		synctest.Wait()
		if len(closed) != 0 {
			t.Fatal("cleanup returned before creation completed")
		}
		if _, err := m.Bucket("cached"); !errors.Is(err, ErrManagerClosed) {
			t.Fatal(err)
		}
		close(release)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		for name, bucket := range buckets {
			if bucket.closes != 1 {
				t.Errorf("%s closes=%d", name, bucket.closes)
			}
		}
	})
}

func TestManagerSameBucketCreationAndPanicRecovery(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var calls atomic.Int32
				entered, release := make(chan struct{}), make(chan struct{})
				bucket := &fakeBucket{}
				m, err := newManager(&config_pb.OSS{Buckets: map[string]*config_pb.OSSBucket{"same": {Driver: "fake", Bucket: "same"}}}, map[string]DriverFactory{"fake": func(BucketConfig) (Bucket, error) {
					if calls.Add(1) == 1 {
						close(entered)
						<-release
						if panics {
							panic("factory panic")
						}
					}
					return bucket, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{}, 9)
				go func() {
					defer func() {
						got := recover()
						if (got != nil) != panics {
							t.Errorf("panic=%v", got)
						}
						done <- struct{}{}
					}()
					if _, err := m.Bucket("same"); err != nil {
						t.Error(err)
					}
				}()
				<-entered
				for range 8 {
					go func() {
						got, err := m.Bucket("same")
						if err != nil || got != bucket {
							t.Errorf("bucket=%v err=%v", got, err)
						}
						done <- struct{}{}
					}()
				}
				synctest.Wait()
				if calls.Load() != 1 {
					t.Fatal("duplicate concurrent factory calls")
				}
				close(release)
				synctest.Wait()
				for range 9 {
					<-done
				}
				want := int32(1)
				if panics {
					want = 2
				}
				if calls.Load() != want {
					t.Fatalf("calls=%d want=%d", calls.Load(), want)
				}
				if err := m.close(); err != nil {
					t.Fatal(err)
				}
				if bucket.closes != 1 {
					t.Fatal(bucket.closes)
				}
			})
		})
	}
}
