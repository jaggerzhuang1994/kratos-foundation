package oss

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// BucketDomainHelper maps object keys to and from a bucket's public base URL.
type BucketDomainHelper struct {
	base *url.URL
}

// NewBucketDomainHelper 校验并标准化 HTTP(S) 存储桶域名。
func NewBucketDomainHelper(domain string) (*BucketDomainHelper, error) {
	domain = strings.TrimSpace(domain)
	base, err := url.Parse(domain)
	if err != nil {
		return nil, fmt.Errorf("parse OSS bucket domain: %w", err)
	}
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("OSS bucket domain must be an absolute HTTP(S) URL")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("OSS bucket domain must not contain userinfo, a query, or a fragment")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = ""
	return &BucketDomainHelper{base: base}, nil
}

// GetFullURL 在配置域名下解析对象键，绝对 URL 必须匹配同一域名和基础路径。
func (helper *BucketDomainHelper) GetFullURL(objectKey string) (string, error) {
	key, err := helper.ParseObjectKey(objectKey)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", errors.New("OSS object key is empty")
	}
	result := *helper.base
	result.Path = strings.TrimRight(helper.base.Path, "/") + "/" + strings.TrimLeft(key, "/")
	result.RawPath = ""
	return result.String(), nil
}

// ParseObjectKey 从相对键或同域绝对 URL 中提取标准化对象键。
func (helper *BucketDomainHelper) ParseObjectKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	// 相对对象键不是 URL 编码文本；字面的 %、?、# 必须在生成 URL 时才转义。
	// 首个路径、查询或片段分隔符之前出现冒号时，仍交给 URL 解析器校验 scheme。
	separator := strings.IndexAny(value, ":/?#")
	if separator < 0 || value[separator] != ':' {
		return strings.TrimLeft(value, "/"), nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse OSS object URL: %w", err)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("OSS object URL must not contain userinfo, a query, or a fragment")
	}
	if parsed.Scheme != helper.base.Scheme || parsed.Host != helper.base.Host {
		return "", errors.New("OSS object URL does not belong to the configured domain")
	}
	basePath := strings.TrimRight(helper.base.Path, "/")
	if parsed.Path != basePath && !strings.HasPrefix(parsed.Path, basePath+"/") {
		return "", errors.New("OSS object URL is outside the configured base path")
	}
	return strings.TrimLeft(strings.TrimPrefix(parsed.Path, basePath), "/"), nil
}
