package aliyun

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	foundationoss "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
)

type recordingObjectClient struct {
	putRequest *aliyunoss.PutObjectRequest
	putResult  *aliyunoss.PutObjectResult
	putErr     error

	getRequest *aliyunoss.GetObjectRequest
	getResult  *aliyunoss.GetObjectResult
	getErr     error

	deleteRequest *aliyunoss.DeleteObjectRequest
	deleteResult  *aliyunoss.DeleteObjectResult
	deleteErr     error

	headRequest *aliyunoss.HeadObjectRequest
	headResult  *aliyunoss.HeadObjectResult
	headErr     error

	listRequest *aliyunoss.ListObjectsV2Request
	listResult  *aliyunoss.ListObjectsV2Result
	listErr     error

	copyRequest *aliyunoss.CopyObjectRequest
	copyResult  *aliyunoss.CopyObjectResult
	copyErr     error
}

func (client *recordingObjectClient) PutObject(
	_ context.Context,
	request *aliyunoss.PutObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.PutObjectResult, error) {
	client.putRequest = request
	return client.putResult, client.putErr
}

func (client *recordingObjectClient) GetObject(
	_ context.Context,
	request *aliyunoss.GetObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.GetObjectResult, error) {
	client.getRequest = request
	return client.getResult, client.getErr
}

func (client *recordingObjectClient) DeleteObject(
	_ context.Context,
	request *aliyunoss.DeleteObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.DeleteObjectResult, error) {
	client.deleteRequest = request
	return client.deleteResult, client.deleteErr
}

func (client *recordingObjectClient) HeadObject(
	_ context.Context,
	request *aliyunoss.HeadObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.HeadObjectResult, error) {
	client.headRequest = request
	return client.headResult, client.headErr
}

func (client *recordingObjectClient) ListObjectsV2(
	_ context.Context,
	request *aliyunoss.ListObjectsV2Request,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.ListObjectsV2Result, error) {
	client.listRequest = request
	return client.listResult, client.listErr
}

func (client *recordingObjectClient) CopyObject(
	_ context.Context,
	request *aliyunoss.CopyObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.CopyObjectResult, error) {
	client.copyRequest = request
	return client.copyResult, client.copyErr
}

func bucketWithClient(client objectClient) *Bucket {
	return &Bucket{client: client, name: "test-bucket"}
}

func TestNewValidatesAndNormalizesConfigurationWithoutNetwork(t *testing.T) {
	for _, config := range []Config{{}, {Region: "cn-hangzhou", Bucket: "bucket"}, {Region: "cn-hangzhou", AccessKeyID: "id", AccessKeySecret: "secret"}} {
		if bucket, err := New(config); err == nil || bucket != nil {
			t.Fatalf("New(%+v) = %v, %v", config, bucket, err)
		}
	}
	bucket, err := New(Config{Region: " cn-hangzhou ", Bucket: " bucket ", AccessKeyID: " id ", AccessKeySecret: " secret "})
	if err != nil || bucket == nil || bucket.name != "bucket" {
		t.Fatalf("New valid config = %#v, %v", bucket, err)
	}
	if _, err := bucket.validateObjectKey(" /object "); err != nil {
		t.Fatal(err)
	}
}

func TestLocalHelpersValidateBoundariesAndDetachMetadata(t *testing.T) {
	end := int64(4)
	if got, err := formatRange(foundationoss.ByteRange{Start: 2, End: &end}); err != nil || got != "bytes=2-4" {
		t.Fatalf("formatRange = %q, %v", got, err)
	}
	if _, err := formatRange(foundationoss.ByteRange{Start: 2, End: int64p(1)}); err == nil {
		t.Fatal("reversed range accepted")
	}
	metadata := map[string]string{"key": "value"}
	copy := cloneMetadata(metadata)
	copy["key"] = "changed"
	if metadata["key"] != "value" || optionalString("") != nil || valueOrUnknown(nil) != -1 {
		t.Fatal("helper behavior is inconsistent")
	}
	if _, err := (*Bucket)(nil).validateObjectKey("key"); err == nil {
		t.Fatal("nil bucket accepted")
	}
}

func TestObjectURLResolvesConfiguredDomain(t *testing.T) {
	domain, err := foundationoss.NewBucketDomainHelper("https://cdn.example.com/assets/")
	if err != nil {
		t.Fatal(err)
	}
	bucket := &Bucket{domain: domain}

	got, err := bucket.ObjectURL(" /reports/annual summary.pdf ")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://cdn.example.com/assets/reports/annual%20summary.pdf"; got != want {
		t.Fatalf("ObjectURL = %q, want %q", got, want)
	}
	if _, err := bucket.ObjectURL("https://other.example.com/object"); err == nil {
		t.Fatal("foreign absolute object URL was accepted")
	}
}

func TestObjectURLRequiresConfiguredDomain(t *testing.T) {
	for _, bucket := range []*Bucket{nil, {}} {
		if _, err := bucket.ObjectURL("object"); !errors.Is(err, foundationoss.ErrPublicDomainUnavailable) {
			t.Fatalf("ObjectURL error = %v, want ErrPublicDomainUnavailable", err)
		}
	}
}

func int64p(value int64) *int64 { return &value }

func TestDriverRegistered(t *testing.T) {
	if !slices.Contains(foundationoss.RegisteredDrivers(), DriverName) {
		t.Fatalf("registered drivers = %v", foundationoss.RegisteredDrivers())
	}
}

func TestOpenRejectsInvalidBucketConfigWithoutNetwork(t *testing.T) {
	validOptions := map[string]string{
		OptionRegion:          "cn-hangzhou",
		OptionAccessKeyID:     "access-key-id",
		OptionAccessKeySecret: "access-key-secret",
	}
	for _, test := range []struct {
		name   string
		config foundationoss.BucketConfig
		want   string
	}{
		{
			name:   "nil options",
			config: foundationoss.BucketConfig{Bucket: "orders"},
			want:   "region is empty",
		},
		{
			name: "missing bucket",
			config: foundationoss.BucketConfig{
				Options: validOptions,
			},
			want: "bucket is empty",
		},
		{
			name: "missing credentials",
			config: foundationoss.BucketConfig{
				Bucket: "orders",
				Options: map[string]string{
					OptionRegion: "cn-hangzhou",
				},
			},
			want: "access key ID and secret are required",
		},
		{
			name: "invalid public domain",
			config: foundationoss.BucketConfig{
				Bucket:  "orders",
				Domain:  "ftp://cdn.example.com/assets",
				Options: validOptions,
			},
			want: "domain",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := open(test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("open(%#v) error = %v, want error containing %q", test.config, err, test.want)
			}
		})
	}
}

func TestOpenConstructsUsableLocalBucketStateWithoutNetwork(t *testing.T) {
	opened, err := open(foundationoss.BucketConfig{
		Name:   "reports",
		Bucket: " physical-reports ",
		Domain: " https://cdn.example.com/assets/ ",
		Options: map[string]string{
			OptionRegion:          " cn-hangzhou ",
			OptionEndpoint:        " https://oss.example.invalid ",
			OptionAccessKeyID:     " access-key-id ",
			OptionAccessKeySecret: " access-key-secret ",
			OptionSecurityToken:   " security-token ",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bucket, ok := opened.(*Bucket)
	if !ok || bucket == nil || bucket.client == nil || bucket.name != "physical-reports" {
		t.Fatalf("open valid config = %#v", opened)
	}
	url, err := bucket.ObjectURL("daily/report 1.csv")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://cdn.example.com/assets/daily/report%201.csv"; url != want {
		t.Fatalf("ObjectURL = %q, want %q", url, want)
	}
}
