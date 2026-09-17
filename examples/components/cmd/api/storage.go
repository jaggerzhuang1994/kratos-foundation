package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/oss/aliyun"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/metrics"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
)

const objectPayload = "Foundation components demo: real OSS bytes.\n"

type objectStorage struct {
	// bucket 借用 OSS Manager 管理的演示存储桶。
	bucket oss.Bucket
}

func newObjectStorage(cfg config.Manager, logger log.Logger, provider metrics.Provider) (*objectStorage, func(), error) {
	manager, cleanup, err := oss.NewManager(cfg, logger)
	if err != nil {
		return nil, nil, err
	}
	observed, err := oss.WithMetrics(manager, provider)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	bucket, err := observed.Bucket("assets")
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return &objectStorage{bucket: bucket}, cleanup, nil
}

// Run 通过真实驱动验证对象闭环；唯一 runID 由调用方提供，避免不同请求覆盖对象。
func (s *objectStorage) Run(ctx context.Context, runID string) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	key := "components/" + runID + ".txt"
	payload := []byte(objectPayload)
	size := int64(len(payload))
	if _, err := s.bucket.PutObject(ctx, key, bytes.NewReader(payload), oss.PutOptions{Size: &size, ContentType: "text/plain"}); err != nil {
		return fmt.Errorf("upload demo object: %w", err)
	}
	// 即使校验失败也释放本轮对象；独立短超时让已取消请求仍可尝试清理。
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer stop()
		if err := s.bucket.DeleteObject(cleanupCtx, key); err != nil {
			result = errors.Join(result, fmt.Errorf("delete demo object: %w", err))
		}
	}()
	info, err := s.bucket.StatObject(ctx, key)
	if err != nil {
		return fmt.Errorf("stat demo object: %w", err)
	}
	if info.Size != size {
		return fmt.Errorf("demo object size: got %d, want %d", info.Size, size)
	}
	exists, err := s.bucket.ObjectExists(ctx, key)
	if err != nil {
		return fmt.Errorf("check demo object: %w", err)
	}
	if !exists {
		return errors.New("demo object disappeared after upload")
	}
	object, err := s.bucket.GetObject(ctx, key, oss.GetOptions{})
	if err != nil {
		return fmt.Errorf("download demo object: %w", err)
	}
	// 多读一个字节识别异常长响应；完整 EOF 与 Close 均实际发生，才能产生成功流指标。
	downloaded, readErr := io.ReadAll(io.LimitReader(object.Body, size+1))
	closeErr := object.Body.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return fmt.Errorf("read demo object: %w", err)
	}
	if !bytes.Equal(downloaded, payload) {
		return errors.New("demo object content mismatch")
	}
	return nil
}
