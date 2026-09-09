package oss

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
)

var (
	// ErrBucketUnknown 表示逻辑 bucket 未配置。
	ErrBucketUnknown = errors.New("OSS bucket is not configured")
	// ErrManagerClosed 表示 Manager 已经关闭。
	ErrManagerClosed = errors.New("OSS manager is closed")
)

type bucketDefinition struct {
	driver string
	config BucketConfig
}

// Manager 按逻辑 bucket 名延迟创建并缓存对象存储实例。
type Manager interface {
	Bucket(string) (Bucket, error)
	BucketNames() []string
}

// manager 持有逻辑 bucket 定义、缓存实例和关闭状态。
type manager struct {
	definitions map[string]bucketDefinition
	drivers     map[string]DriverFactory

	mu      sync.Mutex
	buckets map[string]Bucket
	closed  bool
}

// newManagerWithDrivers 使用固定驱动快照读取配置并组装 Manager。
// 空配置是合法的，便于不使用 OSS 的应用继续复用基础 Wire 集合。
func newManagerWithDrivers(
	configManager config.Manager,
	logger log.Logger,
	registered map[string]DriverFactory,
) (*manager, func(), error) {
	component := new(config_pb.OSS)
	if err := configManager.Load("oss", component, new(config_pb.OSS)); err != nil {
		return nil, nil, fmt.Errorf("load OSS config: %w", err)
	}
	m, err := newManager(component, registered)
	if err != nil {
		return nil, nil, err
	}
	logger = logger.WithModule("oss")
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if closeErr := m.close(); closeErr != nil {
				logger.With("error", closeErr).Error("NewManager | cleanup.failed")
			}
		})
	}
	return m, cleanup, nil
}

// newManager 校验逻辑 bucket 与驱动映射，不在构造阶段访问远程存储。
func newManager(component *config_pb.OSS, registered map[string]DriverFactory) (*manager, error) {
	if component == nil {
		return nil, errors.New("OSS config is nil")
	}
	m := &manager{
		definitions: make(map[string]bucketDefinition, len(component.GetBuckets())),
		drivers:     registered,
		buckets:     make(map[string]Bucket),
	}
	for name, option := range component.GetBuckets() {
		if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) {
			return nil, fmt.Errorf("OSS bucket logical name %q is invalid", name)
		}
		if option == nil {
			return nil, fmt.Errorf("OSS bucket %q config is nil", name)
		}
		driverName := normalizeDriverName(option.GetDriver())
		if driverName == "" {
			return nil, fmt.Errorf("OSS bucket %q driver is empty", name)
		}
		if registered[driverName] == nil {
			return nil, fmt.Errorf("OSS bucket %q uses unregistered driver %q", name, driverName)
		}
		physicalName := strings.TrimSpace(option.GetBucket())
		if physicalName == "" {
			return nil, fmt.Errorf("OSS bucket %q physical bucket is empty", name)
		}
		domain := strings.TrimSpace(option.GetDomain())
		if domain != "" {
			if _, err := NewBucketDomainHelper(domain); err != nil {
				return nil, fmt.Errorf("OSS bucket %q domain: %w", name, err)
			}
		}
		m.definitions[name] = bucketDefinition{
			driver: driverName,
			config: BucketConfig{
				Name:    name,
				Bucket:  physicalName,
				Domain:  domain,
				Options: cloneStrings(option.GetOptions()),
			},
		}
	}
	return m, nil
}

// Bucket 按逻辑名返回已缓存或新创建的 bucket 实例。
func (m *manager) Bucket(name string) (Bucket, error) {
	if m == nil {
		return nil, errors.New("OSS manager is nil")
	}
	name = strings.TrimSpace(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}
	if bucket := m.buckets[name]; bucket != nil {
		return bucket, nil
	}
	definition, exists := m.definitions[name]
	if !exists {
		return nil, fmt.Errorf("OSS bucket %q: %w", name, ErrBucketUnknown)
	}
	factory := m.drivers[definition.driver]
	if factory == nil {
		return nil, fmt.Errorf("OSS bucket %q driver %q is unavailable", name, definition.driver)
	}
	configSnapshot := definition.config
	configSnapshot.Options = cloneStrings(definition.config.Options)
	bucket, err := factory(configSnapshot)
	if err != nil {
		return nil, fmt.Errorf("open OSS bucket %q with driver %q: %w", name, definition.driver, err)
	}
	if bucket == nil {
		return nil, fmt.Errorf("open OSS bucket %q with driver %q: bucket is nil", name, definition.driver)
	}
	m.buckets[name] = bucket
	return bucket, nil
}

// BucketNames 返回已配置逻辑 bucket 名的有序快照。
func (m *manager) BucketNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.definitions))
	for name := range m.definitions {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// close 关闭 Manager 已经创建且实现 io.Closer 的驱动实例。
func (m *manager) close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	buckets := m.buckets
	m.buckets = nil
	m.mu.Unlock()

	var result error
	for name, bucket := range buckets {
		closer, ok := bucket.(io.Closer)
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close OSS bucket %q: %w", name, err))
		}
	}
	return result
}

// cloneStrings 复制字符串 map，防止驱动修改 Manager 持有的配置。
func cloneStrings(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	maps.Copy(result, values)
	return result
}

// NewManager 读取 OSS 配置并固定当前驱动注册表快照。
func NewManager(configManager config.Manager, logger log.Logger) (Manager, func(), error) {
	manager, cleanup, err := newManagerWithDrivers(configManager, logger, driverSnapshot())
	if err != nil {
		return nil, nil, err
	}
	return manager, cleanup, nil
}
