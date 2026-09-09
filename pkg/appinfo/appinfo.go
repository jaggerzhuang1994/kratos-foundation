// Package appinfo 提供不可变的进程身份信息。
package appinfo

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
)

const (
	// MetadataEnvironment 是环境元数据键。
	MetadataEnvironment = "env"
	// MetadataHostname 是主机名元数据键。
	MetadataHostname = "hostname"
)

// AppInfo 暴露单个应用进程的身份和元数据。
type AppInfo interface {
	// ID 返回唯一的进程实例标识。
	ID() string
	// Name 返回可执行程序名称。
	Name() string
	// Version 返回构造时提供的版本。
	Version() string
	// Metadata 返回进程元数据副本。
	Metadata() map[string]string
}

type info struct {
	id       string
	name     string
	version  string
	metadata map[string]string
}

// New 使用当前运行环境生成进程身份；返回值不会随环境变量后续变化。
func New(version string) AppInfo {
	hostname := processHostname
	return &info{
		id:      fmt.Sprintf("%s-%s", hostname, uuid.New().String()),
		name:    processExecutableName,
		version: version,
		metadata: map[string]string{
			MetadataEnvironment: env.AppEnv(),
			MetadataHostname:    hostname,
		},
	}
}

// ID 返回构造时生成的进程实例标识。
func (i *info) ID() string {
	return i.id
}

// Name 返回当前可执行程序名称。
func (i *info) Name() string {
	return i.name
}

// Version 返回调用方在构造时注入的版本。
func (i *info) Version() string {
	return i.version
}

// Metadata 返回元数据副本，防止调用方修改进程身份快照。
func (i *info) Metadata() map[string]string {
	return maps.Clone(i.metadata)
}

var (
	processHostname       = resolveHostname()
	processExecutableName = resolveExecutableName()
)

// resolveHostname 读取系统主机名；系统调用失败时返回稳定占位值。
func resolveHostname() string {
	value, err := os.Hostname()
	if err != nil {
		return "unknown-host"
	}
	return value
}

// resolveExecutableName 优先使用当前可执行文件路径，无法读取时回退到 argv。
func resolveExecutableName() string {
	path, err := os.Executable()
	if err == nil {
		return filepath.Base(path)
	}
	if len(os.Args) > 0 {
		return filepath.Base(os.Args[0])
	}
	return "unknown-executable"
}
