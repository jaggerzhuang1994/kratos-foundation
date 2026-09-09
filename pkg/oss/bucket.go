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

// Object 是对象元信息和需由调用方关闭的流式内容。
type Object struct {
	ObjectInfo
	Body io.ReadCloser
}

// ObjectInfo 是各厂商均可返回的通用对象元信息。
type ObjectInfo struct {
	Key                string
	Size               int64
	ETag               string
	ContentType        string
	ContentEncoding    string
	CacheControl       string
	ContentDisposition string
	StorageClass       string
	LastModified       time.Time
	Metadata           map[string]string
}

// PutOptions 描述上传内容和可移植的 HTTP 元数据。
// Size 为 nil 表示未知；ForbidOverwrite 可用于仅创建语义。
type PutOptions struct {
	Size               *int64
	ContentType        string
	ContentEncoding    string
	CacheControl       string
	ContentDisposition string
	Metadata           map[string]string
	ForbidOverwrite    bool
}

// GetOptions 描述读取选项，Range 为 nil 时读取全部内容。
type GetOptions struct {
	Range *ByteRange
}

// ByteRange 是包含起止位置的字节范围；End 为 nil 表示读到末尾。
type ByteRange struct {
	Start int64
	End   *int64
}

// Lister 是支持分页枚举对象的可选能力。
type Lister interface {
	ListObjects(context.Context, ListOptions) (ListResult, error)
}

// ListOptions 描述通用的前缀、分隔符和游标分页。
type ListOptions struct {
	Prefix    string
	Delimiter string
	Cursor    string
	MaxKeys   int32
}

// ListResult 是一页对象及下一页游标。
type ListResult struct {
	Objects        []ObjectInfo
	CommonPrefixes []string
	NextCursor     string
	Truncated      bool
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
	Metadata        map[string]string
	ReplaceMetadata bool
	ForbidOverwrite bool
}
