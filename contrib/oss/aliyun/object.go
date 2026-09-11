package aliyun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	foundationoss "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
)

// PutObject 流式上传对象并返回可获取的 ETag。
func (bucket *Bucket) PutObject(
	ctx context.Context,
	objectKey string,
	body io.Reader,
	options foundationoss.PutOptions,
) (foundationoss.ObjectInfo, error) {
	key, err := bucket.validateObjectKey(objectKey)
	if err != nil {
		return foundationoss.ObjectInfo{}, err
	}
	if body == nil {
		return foundationoss.ObjectInfo{}, errors.New("Aliyun OSS object body is nil")
	}
	request := &aliyunoss.PutObjectRequest{
		Bucket:             aliyunoss.Ptr(bucket.name),
		Key:                aliyunoss.Ptr(key),
		Body:               body,
		ContentLength:      options.Size,
		ContentType:        optionalString(options.ContentType),
		ContentEncoding:    optionalString(options.ContentEncoding),
		CacheControl:       optionalString(options.CacheControl),
		ContentDisposition: optionalString(options.ContentDisposition),
		Metadata:           cloneMetadata(options.Metadata),
	}
	if options.ForbidOverwrite {
		request.ForbidOverwrite = aliyunoss.Ptr("true")
	}
	result, err := bucket.client.PutObject(ctx, request)
	if err != nil {
		return foundationoss.ObjectInfo{}, wrapProviderError("put", key, err)
	}
	if result == nil {
		return foundationoss.ObjectInfo{}, fmt.Errorf("put Aliyun OSS object %q: response is nil", key)
	}
	return foundationoss.ObjectInfo{
		Key:                key,
		Size:               valueOrUnknown(options.Size),
		ETag:               aliyunoss.ToString(result.ETag),
		ContentType:        options.ContentType,
		ContentEncoding:    options.ContentEncoding,
		CacheControl:       options.CacheControl,
		ContentDisposition: options.ContentDisposition,
		Metadata:           cloneMetadata(options.Metadata),
	}, nil
}

// GetObject 流式读取完整对象或指定字节范围。
func (bucket *Bucket) GetObject(
	ctx context.Context,
	objectKey string,
	options foundationoss.GetOptions,
) (*foundationoss.Object, error) {
	key, err := bucket.validateObjectKey(objectKey)
	if err != nil {
		return nil, err
	}
	request := &aliyunoss.GetObjectRequest{
		Bucket: aliyunoss.Ptr(bucket.name),
		Key:    aliyunoss.Ptr(key),
	}
	if options.Range != nil {
		rangeHeader, rangeErr := formatRange(*options.Range)
		if rangeErr != nil {
			return nil, rangeErr
		}
		request.Range = aliyunoss.Ptr(rangeHeader)
		request.RangeBehavior = aliyunoss.Ptr("standard")
	}
	result, err := bucket.client.GetObject(ctx, request)
	if err != nil {
		return nil, wrapProviderError("get", key, err)
	}
	if result == nil || result.Body == nil {
		return nil, fmt.Errorf("get Aliyun OSS object %q: response body is nil", key)
	}
	return &foundationoss.Object{
		ObjectInfo: foundationoss.ObjectInfo{
			Key:         key,
			Size:        result.ContentLength,
			ETag:        aliyunoss.ToString(result.ETag),
			ContentType: aliyunoss.ToString(result.ContentType),
			// SDK 的 GET 结果未单独暴露这些属性，从原始响应头保留对象元信息。
			ContentEncoding:    result.Headers.Get("Content-Encoding"),
			CacheControl:       result.Headers.Get("Cache-Control"),
			ContentDisposition: result.Headers.Get("Content-Disposition"),
			StorageClass:       aliyunoss.ToString(result.StorageClass),
			LastModified:       aliyunoss.ToTime(result.LastModified),
			Metadata:           cloneMetadata(result.Metadata),
		},
		Body: result.Body,
	}, nil
}

// DeleteObject 幂等删除指定对象。
func (bucket *Bucket) DeleteObject(ctx context.Context, objectKey string) error {
	key, err := bucket.validateObjectKey(objectKey)
	if err != nil {
		return err
	}
	_, err = bucket.client.DeleteObject(ctx, &aliyunoss.DeleteObjectRequest{
		Bucket: aliyunoss.Ptr(bucket.name),
		Key:    aliyunoss.Ptr(key),
	})
	if err != nil {
		return wrapProviderError("delete", key, err)
	}
	return nil
}

// StatObject 返回对象元信息而不下载内容。
func (bucket *Bucket) StatObject(ctx context.Context, objectKey string) (foundationoss.ObjectInfo, error) {
	key, err := bucket.validateObjectKey(objectKey)
	if err != nil {
		return foundationoss.ObjectInfo{}, err
	}
	result, err := bucket.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(bucket.name),
		Key:    aliyunoss.Ptr(key),
	})
	if err != nil {
		return foundationoss.ObjectInfo{}, wrapProviderError("stat", key, err)
	}
	if result == nil {
		return foundationoss.ObjectInfo{}, fmt.Errorf("stat Aliyun OSS object %q: response is nil", key)
	}
	return objectInfoFromHead(key, result), nil
}

// ObjectExists 判断对象是否存在，仅把明确的 404 视为 false。
func (bucket *Bucket) ObjectExists(ctx context.Context, objectKey string) (bool, error) {
	_, err := bucket.StatObject(ctx, objectKey)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, foundationoss.ErrObjectNotFound) {
		return false, nil
	}
	return false, err
}

// ListObjects 按前缀和游标分页枚举对象。
func (bucket *Bucket) ListObjects(ctx context.Context, options foundationoss.ListOptions) (foundationoss.ListResult, error) {
	if err := bucket.validateInitialized(); err != nil {
		return foundationoss.ListResult{}, err
	}
	if options.MaxKeys < 0 {
		return foundationoss.ListResult{}, errors.New("Aliyun OSS list max keys must not be negative")
	}
	result, err := bucket.client.ListObjectsV2(ctx, &aliyunoss.ListObjectsV2Request{
		Bucket:            aliyunoss.Ptr(bucket.name),
		Prefix:            optionalString(options.Prefix),
		Delimiter:         optionalString(options.Delimiter),
		ContinuationToken: optionalString(options.Cursor),
		MaxKeys:           options.MaxKeys,
	})
	if err != nil {
		return foundationoss.ListResult{}, fmt.Errorf("list Aliyun OSS bucket %q: %w", bucket.name, err)
	}
	if result == nil {
		return foundationoss.ListResult{}, fmt.Errorf("list Aliyun OSS bucket %q: response is nil", bucket.name)
	}
	page := foundationoss.ListResult{
		Objects:        make([]foundationoss.ObjectInfo, 0, len(result.Contents)),
		CommonPrefixes: make([]string, 0, len(result.CommonPrefixes)),
		NextCursor:     aliyunoss.ToString(result.NextContinuationToken),
		Truncated:      result.IsTruncated,
	}
	for _, object := range result.Contents {
		page.Objects = append(page.Objects, foundationoss.ObjectInfo{
			Key:          aliyunoss.ToString(object.Key),
			Size:         object.Size,
			ETag:         aliyunoss.ToString(object.ETag),
			StorageClass: aliyunoss.ToString(object.StorageClass),
			LastModified: aliyunoss.ToTime(object.LastModified),
		})
	}
	for _, prefix := range result.CommonPrefixes {
		page.CommonPrefixes = append(page.CommonPrefixes, aliyunoss.ToString(prefix.Prefix))
	}
	return page, nil
}

// CopyObject 在当前 bucket 内执行服务端对象复制。
func (bucket *Bucket) CopyObject(
	ctx context.Context,
	sourceKey string,
	destinationKey string,
	options foundationoss.CopyOptions,
) (foundationoss.ObjectInfo, error) {
	source, err := bucket.validateObjectKey(sourceKey)
	if err != nil {
		return foundationoss.ObjectInfo{}, err
	}
	destination, err := bucket.validateObjectKey(destinationKey)
	if err != nil {
		return foundationoss.ObjectInfo{}, err
	}
	request := &aliyunoss.CopyObjectRequest{
		Bucket:       aliyunoss.Ptr(bucket.name),
		Key:          aliyunoss.Ptr(destination),
		SourceBucket: aliyunoss.Ptr(bucket.name),
		SourceKey:    aliyunoss.Ptr(source),
	}
	if options.ForbidOverwrite {
		request.ForbidOverwrite = aliyunoss.Ptr("true")
	}
	if options.ReplaceMetadata {
		request.MetadataDirective = aliyunoss.Ptr("REPLACE")
		request.Metadata = cloneMetadata(options.Metadata)
	}
	result, err := bucket.client.CopyObject(ctx, request)
	if err != nil {
		return foundationoss.ObjectInfo{}, wrapProviderError("copy", destination, err)
	}
	if result == nil {
		return foundationoss.ObjectInfo{}, fmt.Errorf("copy Aliyun OSS object %q: response is nil", destination)
	}
	info := foundationoss.ObjectInfo{
		Key:          destination,
		ETag:         aliyunoss.ToString(result.ETag),
		LastModified: aliyunoss.ToTime(result.LastModified),
	}
	if options.ReplaceMetadata {
		info.Metadata = cloneMetadata(options.Metadata)
	}
	return info, nil
}

// validateObjectKey 校验 bucket 初始化状态并规范化对象键。
func (bucket *Bucket) validateObjectKey(objectKey string) (string, error) {
	if err := bucket.validateInitialized(); err != nil {
		return "", err
	}
	key := strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if key == "" {
		return "", errors.New("Aliyun OSS object key is empty")
	}
	return key, nil
}

// validateInitialized 校验 bucket 初始化状态；Context 的取消由 SDK 请求处理。
func (bucket *Bucket) validateInitialized() error {
	if bucket == nil || bucket.client == nil || bucket.name == "" {
		return errors.New("Aliyun OSS bucket is not initialized")
	}
	return nil
}

// formatRange 把通用字节范围转为 HTTP Range 头。
func formatRange(value foundationoss.ByteRange) (string, error) {
	if value.Start < 0 {
		return "", errors.New("Aliyun OSS byte range start must not be negative")
	}
	if value.End != nil && *value.End < value.Start {
		return "", errors.New("Aliyun OSS byte range end must not precede start")
	}
	if value.End == nil {
		return fmt.Sprintf("bytes=%d-", value.Start), nil
	}
	return fmt.Sprintf("bytes=%d-%d", value.Start, *value.End), nil
}

// objectInfoFromHead 将阿里云 HEAD 响应转为通用元信息。
func objectInfoFromHead(key string, result *aliyunoss.HeadObjectResult) foundationoss.ObjectInfo {
	if result == nil {
		return foundationoss.ObjectInfo{Key: key}
	}
	return foundationoss.ObjectInfo{
		Key:                key,
		Size:               result.ContentLength,
		ETag:               aliyunoss.ToString(result.ETag),
		ContentType:        aliyunoss.ToString(result.ContentType),
		ContentEncoding:    aliyunoss.ToString(result.ContentEncoding),
		CacheControl:       aliyunoss.ToString(result.CacheControl),
		ContentDisposition: aliyunoss.ToString(result.ContentDisposition),
		StorageClass:       aliyunoss.ToString(result.StorageClass),
		LastModified:       aliyunoss.ToTime(result.LastModified),
		Metadata:           cloneMetadata(result.Metadata),
	}
}

// wrapProviderError 保留云厂商错误链并映射通用错误语义。
func wrapProviderError(operation string, key string, err error) error {
	var serviceError *aliyunoss.ServiceError
	if errors.As(err, &serviceError) {
		switch serviceError.StatusCode {
		case http.StatusNotFound:
			return fmt.Errorf("%s Aliyun OSS object %q: %w: %w", operation, key, foundationoss.ErrObjectNotFound, err)
		case http.StatusConflict, http.StatusPreconditionFailed:
			return fmt.Errorf("%s Aliyun OSS object %q: %w: %w", operation, key, foundationoss.ErrObjectAlreadyExists, err)
		}
	}
	return fmt.Errorf("%s Aliyun OSS object %q: %w", operation, key, err)
}

// optionalString 把空字符串转为 nil，避免发送无意义的空请求头。
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aliyunoss.Ptr(value)
}

// valueOrUnknown 将未知大小表示为 -1。
func valueOrUnknown(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

// cloneMetadata 复制元数据，避免 SDK 或调用方共享可变 map。
func cloneMetadata(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	maps.Copy(result, values)
	return result
}
