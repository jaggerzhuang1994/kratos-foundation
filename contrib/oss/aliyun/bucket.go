// Package aliyun implements and registers the Alibaba Cloud OSS driver.
package aliyun

import (
	"context"
	"errors"
	"fmt"
	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	foundationoss "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
	"strings"
)

const (
	// DriverName 是配置中使用的阿里云 OSS 驱动名。
	DriverName = "aliyun"
	// OptionRegion 是阿里云 region 配置 key。
	OptionRegion = "region"
	// OptionEndpoint 是可选自定义 endpoint 配置 key。
	OptionEndpoint = "endpoint"
	// OptionAccessKeyID 是 AccessKey ID 配置 key。
	OptionAccessKeyID = "access_key_id"
	// OptionAccessKeySecret 是 AccessKey Secret 配置 key。
	OptionAccessKeySecret = "access_key_secret"
	// OptionSecurityToken 是可选 STS token 配置 key。
	OptionSecurityToken = "security_token"
)

// Config contains the provider-specific settings needed by Alibaba Cloud OSS.
type Config struct {
	Region          string
	Endpoint        string
	Bucket          string
	Domain          string
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
}

type objectClient interface {
	PutObject(context.Context, *aliyunoss.PutObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error)
	GetObject(context.Context, *aliyunoss.GetObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error)
	DeleteObject(context.Context, *aliyunoss.DeleteObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error)
	HeadObject(context.Context, *aliyunoss.HeadObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error)
	ListObjectsV2(context.Context, *aliyunoss.ListObjectsV2Request, ...func(*aliyunoss.Options)) (*aliyunoss.ListObjectsV2Result, error)
	CopyObject(context.Context, *aliyunoss.CopyObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.CopyObjectResult, error)
}

// Bucket 封装一个阿里云 OSS bucket 的通用对象操作。
type Bucket struct {
	client objectClient
	name   string
	domain *foundationoss.BucketDomainHelper
}

// init 在导入 contrib 包时向核心 OSS 注册阿里云驱动。
func init() {
	foundationoss.MustRegisterDriver(DriverName, open)
}

// open 将通用 bucket 配置转换为阿里云配置。
func open(config foundationoss.BucketConfig) (foundationoss.Bucket, error) {
	return New(Config{
		Region:          config.Options[OptionRegion],
		Endpoint:        config.Options[OptionEndpoint],
		Bucket:          config.Bucket,
		Domain:          config.Domain,
		AccessKeyID:     config.Options[OptionAccessKeyID],
		AccessKeySecret: config.Options[OptionAccessKeySecret],
		SecurityToken:   config.Options[OptionSecurityToken],
	})
}

// New 校验配置并创建阿里云 OSS bucket 实例。
func New(config Config) (*Bucket, error) {
	config.Region = strings.TrimSpace(config.Region)
	config.Endpoint = strings.TrimSpace(config.Endpoint)
	config.Bucket = strings.TrimSpace(config.Bucket)
	config.Domain = strings.TrimSpace(config.Domain)
	config.AccessKeyID = strings.TrimSpace(config.AccessKeyID)
	config.AccessKeySecret = strings.TrimSpace(config.AccessKeySecret)
	config.SecurityToken = strings.TrimSpace(config.SecurityToken)
	if config.Region == "" {
		return nil, errors.New("Aliyun OSS region is empty")
	}
	if config.Bucket == "" {
		return nil, errors.New("Aliyun OSS bucket is empty")
	}
	if config.AccessKeyID == "" || config.AccessKeySecret == "" {
		return nil, errors.New("Aliyun OSS access key ID and secret are required")
	}
	var domain *foundationoss.BucketDomainHelper
	if config.Domain != "" {
		var err error
		domain, err = foundationoss.NewBucketDomainHelper(config.Domain)
		if err != nil {
			return nil, fmt.Errorf("Aliyun OSS domain: %w", err)
		}
	}

	provider := credentials.NewStaticCredentialsProvider(
		config.AccessKeyID,
		config.AccessKeySecret,
		config.SecurityToken,
	)
	sdkConfig := aliyunoss.LoadDefaultConfig().
		WithRegion(config.Region).
		WithCredentialsProvider(provider)
	if config.Endpoint != "" {
		sdkConfig = sdkConfig.WithEndpoint(config.Endpoint)
	}
	return &Bucket{client: aliyunoss.NewClient(sdkConfig), name: config.Bucket, domain: domain}, nil
}

// ObjectURL 使用配置的公开域名生成对象 URL。
func (bucket *Bucket) ObjectURL(objectKey string) (string, error) {
	if bucket == nil || bucket.domain == nil {
		return "", foundationoss.ErrPublicDomainUnavailable
	}
	return bucket.domain.GetFullURL(objectKey)
}

var (
	_ foundationoss.Bucket      = (*Bucket)(nil)
	_ foundationoss.Lister      = (*Bucket)(nil)
	_ foundationoss.Copier      = (*Bucket)(nil)
	_ foundationoss.URLResolver = (*Bucket)(nil)
)
