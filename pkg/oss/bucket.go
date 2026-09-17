// Package oss defines provider-independent object storage contracts and drivers.
package oss

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// ErrObjectNotFound 表示对象不存在。
	ErrObjectNotFound = errors.New("OSS object not found")
	// ErrObjectAlreadyExists 表示禁止覆盖时目标对象已存在。
	ErrObjectAlreadyExists = errors.New("OSS object already exists")
	// ErrPublicDomainUnavailable 表示 bucket 未配置公开访问域名。
	ErrPublicDomainUnavailable = errors.New("OSS public domain is not configured")
)

// Bucket 是各对象存储驱动都必须实现的流式 CRUD 契约。
// PutObject 默认是 upsert，DeleteObject 按各主流对象存储的语义保持幂等。
type Bucket interface {
	PutObject(context.Context, string, io.Reader, PutOptions) (ObjectInfo, error)
	GetObject(context.Context, string, GetOptions) (*Object, error)
	DeleteObject(context.Context, string) error
	StatObject(context.Context, string) (ObjectInfo, error)
	ObjectExists(context.Context, string) (bool, error)
}

// Object 表示流式下载结果。
type Object struct {
	// ObjectInfo 当前对象的通用元信息。
	ObjectInfo
	// Body 对象内容流，使用结束后须由调用方关闭。
	Body io.ReadCloser
}

// ObjectInfo 是各厂商均可返回的通用对象元信息。
type ObjectInfo struct {
	// Key 对象在 bucket 中的键。
	Key string
	// Size 内容字节数；范围读取时可为返回片段大小，驱动无法获知时可为 -1。
	Size int64
	// ETag 服务端返回的实体标识，含义由存储服务决定。
	ETag string
	// ContentType 内容的 MIME 类型。
	ContentType string
	// ContentEncoding 内容编码。
	ContentEncoding string
	// CacheControl HTTP 缓存控制信息。
	CacheControl string
	// ContentDisposition HTTP 内容展示或下载方式。
	ContentDisposition string
	// StorageClass 存储服务返回的存储级别。
	StorageClass string
	// LastModified 对象最后修改时间。
	LastModified time.Time
	// Metadata 对象的自定义元数据。
	Metadata map[string]string
}

// PutOptions 配置对象上传。
type PutOptions struct {
	// Size 上传字节数；nil 表示长度未知。
	Size *int64
	// ContentType 上传内容的 MIME 类型。
	ContentType string
	// ContentEncoding 上传内容的编码。
	ContentEncoding string
	// CacheControl 对象的 HTTP 缓存控制信息。
	CacheControl string
	// ContentDisposition 对象的 HTTP 展示或下载方式。
	ContentDisposition string
	// Metadata 随对象保存的自定义元数据。
	Metadata map[string]string
	// ForbidOverwrite 为 true 时禁止覆盖已有对象。
	ForbidOverwrite bool
}

// GetOptions 配置对象读取。
type GetOptions struct {
	// Range 读取字节范围；nil 表示完整读取。
	Range *ByteRange
}

// ByteRange 表示读取的字节区间。
type ByteRange struct {
	// Start 包含在结果内的起始字节偏移，应为非负数。
	Start int64
	// End 包含在结果内的结束偏移，不得小于 Start；nil 表示读取到末尾。
	End *int64
}

// Lister 是支持分页枚举对象的可选能力。
type Lister interface {
	ListObjects(context.Context, ListOptions) (ListResult, error)
}

// ListOptions 配置对象分页查询。
type ListOptions struct {
	// Prefix 只列出具有此前缀的对象。
	Prefix string
	// Delimiter 用于归并公共前缀的分隔符。
	Delimiter string
	// Cursor 上一页返回的不透明游标。
	Cursor string
	// MaxKeys 单页数量上限，默认和限制由驱动处理。
	MaxKeys int32
}

// ListResult 表示一页对象查询结果。
type ListResult struct {
	// Objects 当前页的对象元信息。
	Objects []ObjectInfo
	// CommonPrefixes 按分隔符归并的公共前缀。
	CommonPrefixes []string
	// NextCursor 请求下一页时原样传回的游标。
	NextCursor string
	// Truncated 是否仍有未返回的结果。
	Truncated bool
}

// Copier 是支持服务端同 bucket 复制的可选能力。
type Copier interface {
	CopyObject(context.Context, string, string, CopyOptions) (ObjectInfo, error)
}

// URLResolver 是支持根据公开域名生成对象 URL 的可选能力。
// 这不等价于临时签名 URL，仅适用于公开 bucket 或 CDN 域名。
type URLResolver interface {
	ObjectURL(string) (string, error)
}

// CopyOptions 描述目标对象的覆盖和元数据策略。
type CopyOptions struct {
	// Metadata 启用 ReplaceMetadata 时写入目标的元数据。
	Metadata map[string]string
	// ReplaceMetadata 是否替换元数据；false 时沿用源对象元数据。
	ReplaceMetadata bool
	// ForbidOverwrite 是否禁止覆盖目标已有对象。
	ForbidOverwrite bool
}
