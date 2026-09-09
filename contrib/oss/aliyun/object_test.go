package aliyun

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	foundationoss "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/oss"
)

func TestPutObjectMapsRequestAndResult(t *testing.T) {
	size := int64(7)
	metadata := map[string]string{"trace": "original"}
	client := &recordingObjectClient{
		putResult: &aliyunoss.PutObjectResult{ETag: aliyunoss.Ptr("etag-put")},
	}
	bucket := bucketWithClient(client)

	info, err := bucket.PutObject(
		context.Background(),
		" /dir/object.txt ",
		strings.NewReader("payload"),
		foundationoss.PutOptions{
			Size:               &size,
			ContentType:        "text/plain",
			ContentEncoding:    "gzip",
			CacheControl:       "max-age=60",
			ContentDisposition: "attachment",
			Metadata:           metadata,
			ForbidOverwrite:    true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := client.putRequest
	if request == nil {
		t.Fatal("PutObject did not call the provider")
	}
	if got := aliyunoss.ToString(request.Bucket); got != "test-bucket" {
		t.Fatalf("request bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.Key); got != "dir/object.txt" {
		t.Fatalf("request key = %q", got)
	}
	if request.ContentLength == nil || *request.ContentLength != 7 {
		t.Fatalf("request content length = %v", request.ContentLength)
	}
	if got := aliyunoss.ToString(request.ContentType); got != "text/plain" {
		t.Fatalf("request content type = %q", got)
	}
	if got := aliyunoss.ToString(request.ContentEncoding); got != "gzip" {
		t.Fatalf("request content encoding = %q", got)
	}
	if got := aliyunoss.ToString(request.CacheControl); got != "max-age=60" {
		t.Fatalf("request cache control = %q", got)
	}
	if got := aliyunoss.ToString(request.ContentDisposition); got != "attachment" {
		t.Fatalf("request content disposition = %q", got)
	}
	if got := aliyunoss.ToString(request.ForbidOverwrite); got != "true" {
		t.Fatalf("request forbid overwrite = %q", got)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "payload" {
		t.Fatalf("request body = %q", got)
	}
	if info.Key != "dir/object.txt" || info.Size != 7 || info.ETag != "etag-put" ||
		info.ContentType != "text/plain" || info.ContentEncoding != "gzip" ||
		info.CacheControl != "max-age=60" || info.ContentDisposition != "attachment" {
		t.Fatalf("PutObject info = %+v", info)
	}
	metadata["trace"] = "changed"
	if !maps.Equal(request.Metadata, map[string]string{"trace": "original"}) ||
		!maps.Equal(info.Metadata, map[string]string{"trace": "original"}) {
		t.Fatalf("metadata was not detached: request=%v info=%v", request.Metadata, info.Metadata)
	}
}

func TestPutObjectRejectsNilResponsesAndMapsProviderConflicts(t *testing.T) {
	t.Run("nil body", func(t *testing.T) {
		client := &recordingObjectClient{}
		_, err := bucketWithClient(client).PutObject(context.Background(), "object", nil, foundationoss.PutOptions{})
		if err == nil || client.putRequest != nil {
			t.Fatalf("PutObject(nil body) error = %v, request = %#v", err, client.putRequest)
		}
	})

	t.Run("nil response", func(t *testing.T) {
		client := &recordingObjectClient{}
		_, err := bucketWithClient(client).PutObject(context.Background(), "object", strings.NewReader("x"), foundationoss.PutOptions{})
		if err == nil {
			t.Fatal("nil provider response was accepted")
		}
	})

	t.Run("provider conflict", func(t *testing.T) {
		providerErr := &aliyunoss.ServiceError{StatusCode: http.StatusConflict, Code: "ObjectAlreadyExists"}
		client := &recordingObjectClient{putErr: providerErr}
		_, err := bucketWithClient(client).PutObject(context.Background(), "object", strings.NewReader("x"), foundationoss.PutOptions{})
		if !errors.Is(err, foundationoss.ErrObjectAlreadyExists) || !errors.Is(err, providerErr) {
			t.Fatalf("PutObject conflict error = %v", err)
		}
	})
}

func TestGetObjectMapsRangeRequestAndResult(t *testing.T) {
	end := int64(5)
	lastModified := time.Date(2026, time.August, 31, 9, 30, 0, 0, time.UTC)
	metadata := map[string]string{"trace": "original"}
	client := &recordingObjectClient{
		getResult: &aliyunoss.GetObjectResult{
			ContentLength: 4,
			ContentType:   aliyunoss.Ptr("text/plain"),
			ResultCommon: aliyunoss.ResultCommon{Headers: http.Header{
				"Content-Encoding":    {"gzip"},
				"Cache-Control":       {"max-age=60"},
				"Content-Disposition": {"attachment"},
			}},
			ETag:         aliyunoss.Ptr("etag-get"),
			LastModified: &lastModified,
			StorageClass: aliyunoss.Ptr("Standard"),
			Metadata:     metadata,
			Body:         io.NopCloser(strings.NewReader("data")),
		},
	}
	bucket := bucketWithClient(client)

	object, err := bucket.GetObject(context.Background(), " /dir/object.txt ", foundationoss.GetOptions{
		Range: &foundationoss.ByteRange{Start: 2, End: &end},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := client.getRequest
	if request == nil {
		t.Fatal("GetObject did not call the provider")
	}
	if got := aliyunoss.ToString(request.Bucket); got != "test-bucket" {
		t.Fatalf("request bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.Key); got != "dir/object.txt" {
		t.Fatalf("request key = %q", got)
	}
	if got := aliyunoss.ToString(request.Range); got != "bytes=2-5" {
		t.Fatalf("request range = %q", got)
	}
	if got := aliyunoss.ToString(request.RangeBehavior); got != "standard" {
		t.Fatalf("request range behavior = %q", got)
	}
	body, err := io.ReadAll(object.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := object.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "data" {
		t.Fatalf("object body = %q", got)
	}
	if object.Key != "dir/object.txt" || object.Size != 4 || object.ETag != "etag-get" ||
		object.ContentType != "text/plain" || object.StorageClass != "Standard" ||
		object.ContentEncoding != "gzip" || object.CacheControl != "max-age=60" ||
		object.ContentDisposition != "attachment" ||
		!object.LastModified.Equal(lastModified) {
		t.Fatalf("GetObject result = %+v", object.ObjectInfo)
	}
	metadata["trace"] = "changed"
	if !maps.Equal(object.Metadata, map[string]string{"trace": "original"}) {
		t.Fatalf("result metadata was not detached: %v", object.Metadata)
	}
}

func TestGetObjectRejectsInvalidResponsesAndMapsProviderNotFound(t *testing.T) {
	t.Run("invalid range", func(t *testing.T) {
		client := &recordingObjectClient{}
		_, err := bucketWithClient(client).GetObject(context.Background(), "object", foundationoss.GetOptions{
			Range: &foundationoss.ByteRange{Start: 2, End: int64p(1)},
		})
		if err == nil || client.getRequest != nil {
			t.Fatalf("GetObject(invalid range) error = %v, request = %#v", err, client.getRequest)
		}
	})

	for _, test := range []struct {
		name   string
		result *aliyunoss.GetObjectResult
	}{
		{name: "nil response"},
		{name: "nil body", result: &aliyunoss.GetObjectResult{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingObjectClient{getResult: test.result}
			if _, err := bucketWithClient(client).GetObject(context.Background(), "object", foundationoss.GetOptions{}); err == nil {
				t.Fatalf("%s was accepted", test.name)
			}
		})
	}

	t.Run("provider not found", func(t *testing.T) {
		providerErr := &aliyunoss.ServiceError{StatusCode: http.StatusNotFound, Code: "NoSuchKey"}
		client := &recordingObjectClient{getErr: providerErr}
		_, err := bucketWithClient(client).GetObject(context.Background(), "object", foundationoss.GetOptions{})
		if !errors.Is(err, foundationoss.ErrObjectNotFound) || !errors.Is(err, providerErr) {
			t.Fatalf("GetObject not-found error = %v", err)
		}
	})
}

func TestDeleteObjectMapsRequestAndProviderErrors(t *testing.T) {
	t.Run("success accepts empty provider response", func(t *testing.T) {
		client := &recordingObjectClient{}
		if err := bucketWithClient(client).DeleteObject(context.Background(), " /dir/object.txt "); err != nil {
			t.Fatal(err)
		}
		if client.deleteRequest == nil {
			t.Fatal("DeleteObject did not call the provider")
		}
		if got := aliyunoss.ToString(client.deleteRequest.Bucket); got != "test-bucket" {
			t.Fatalf("request bucket = %q", got)
		}
		if got := aliyunoss.ToString(client.deleteRequest.Key); got != "dir/object.txt" {
			t.Fatalf("request key = %q", got)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		providerErr := errors.New("delete unavailable")
		client := &recordingObjectClient{deleteErr: providerErr}
		err := bucketWithClient(client).DeleteObject(context.Background(), "object")
		if !errors.Is(err, providerErr) {
			t.Fatalf("DeleteObject error = %v", err)
		}
	})
}

func TestStatObjectMapsRequestAndResult(t *testing.T) {
	lastModified := time.Date(2026, time.August, 31, 10, 15, 0, 0, time.UTC)
	metadata := map[string]string{"trace": "original"}
	client := &recordingObjectClient{
		headResult: &aliyunoss.HeadObjectResult{
			ContentLength:      23,
			ETag:               aliyunoss.Ptr("etag-head"),
			ContentType:        aliyunoss.Ptr("application/json"),
			ContentEncoding:    aliyunoss.Ptr("br"),
			CacheControl:       aliyunoss.Ptr("no-cache"),
			ContentDisposition: aliyunoss.Ptr("inline"),
			StorageClass:       aliyunoss.Ptr("IA"),
			LastModified:       &lastModified,
			Metadata:           metadata,
		},
	}
	bucket := bucketWithClient(client)

	info, err := bucket.StatObject(context.Background(), " /dir/object.json ")
	if err != nil {
		t.Fatal(err)
	}
	request := client.headRequest
	if request == nil {
		t.Fatal("StatObject did not call the provider")
	}
	if got := aliyunoss.ToString(request.Bucket); got != "test-bucket" {
		t.Fatalf("request bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.Key); got != "dir/object.json" {
		t.Fatalf("request key = %q", got)
	}
	if info.Key != "dir/object.json" || info.Size != 23 || info.ETag != "etag-head" ||
		info.ContentType != "application/json" || info.ContentEncoding != "br" ||
		info.CacheControl != "no-cache" || info.ContentDisposition != "inline" ||
		info.StorageClass != "IA" || !info.LastModified.Equal(lastModified) {
		t.Fatalf("StatObject info = %+v", info)
	}
	metadata["trace"] = "changed"
	if !maps.Equal(info.Metadata, map[string]string{"trace": "original"}) {
		t.Fatalf("result metadata was not detached: %v", info.Metadata)
	}
}

func TestStatObjectRejectsNilResponseAndPreservesProviderError(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		client := &recordingObjectClient{}
		if _, err := bucketWithClient(client).StatObject(context.Background(), "object"); err == nil {
			t.Fatal("nil provider response was accepted")
		}
	})

	t.Run("provider error", func(t *testing.T) {
		providerErr := errors.New("head unavailable")
		client := &recordingObjectClient{headErr: providerErr}
		_, err := bucketWithClient(client).StatObject(context.Background(), "object")
		if !errors.Is(err, providerErr) {
			t.Fatalf("StatObject error = %v", err)
		}
	})
}

func TestObjectExistsDistinguishesNotFoundFromProviderFailure(t *testing.T) {
	t.Run("exists", func(t *testing.T) {
		client := &recordingObjectClient{headResult: &aliyunoss.HeadObjectResult{}}
		exists, err := bucketWithClient(client).ObjectExists(context.Background(), "object")
		if err != nil || !exists {
			t.Fatalf("ObjectExists = %t, %v", exists, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		providerErr := &aliyunoss.ServiceError{StatusCode: http.StatusNotFound, Code: "NoSuchKey"}
		client := &recordingObjectClient{headErr: providerErr}
		exists, err := bucketWithClient(client).ObjectExists(context.Background(), "object")
		if err != nil || exists {
			t.Fatalf("ObjectExists(404) = %t, %v", exists, err)
		}
	})

	t.Run("provider failure", func(t *testing.T) {
		providerErr := errors.New("head unavailable")
		client := &recordingObjectClient{headErr: providerErr}
		exists, err := bucketWithClient(client).ObjectExists(context.Background(), "object")
		if exists || !errors.Is(err, providerErr) {
			t.Fatalf("ObjectExists(provider failure) = %t, %v", exists, err)
		}
	})
}

func TestListObjectsMapsRequestAndPage(t *testing.T) {
	lastModified := time.Date(2026, time.August, 31, 11, 0, 0, 0, time.UTC)
	client := &recordingObjectClient{
		listResult: &aliyunoss.ListObjectsV2Result{
			Contents: []aliyunoss.ObjectProperties{
				{
					Key:          aliyunoss.Ptr("reports/annual.json"),
					Size:         42,
					ETag:         aliyunoss.Ptr("etag-list"),
					StorageClass: aliyunoss.Ptr("Standard"),
					LastModified: &lastModified,
				},
			},
			CommonPrefixes:        []aliyunoss.CommonPrefix{{Prefix: aliyunoss.Ptr("reports/archive/")}},
			NextContinuationToken: aliyunoss.Ptr("cursor-next"),
			IsTruncated:           true,
		},
	}
	bucket := bucketWithClient(client)

	page, err := bucket.ListObjects(context.Background(), foundationoss.ListOptions{
		Prefix:    "reports/",
		Delimiter: "/",
		Cursor:    "cursor-current",
		MaxKeys:   25,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := client.listRequest
	if request == nil {
		t.Fatal("ListObjects did not call the provider")
	}
	if got := aliyunoss.ToString(request.Bucket); got != "test-bucket" {
		t.Fatalf("request bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.Prefix); got != "reports/" {
		t.Fatalf("request prefix = %q", got)
	}
	if got := aliyunoss.ToString(request.Delimiter); got != "/" {
		t.Fatalf("request delimiter = %q", got)
	}
	if got := aliyunoss.ToString(request.ContinuationToken); got != "cursor-current" {
		t.Fatalf("request cursor = %q", got)
	}
	if request.MaxKeys != 25 {
		t.Fatalf("request max keys = %d", request.MaxKeys)
	}
	if len(page.Objects) != 1 {
		t.Fatalf("objects = %+v", page.Objects)
	}
	object := page.Objects[0]
	if object.Key != "reports/annual.json" || object.Size != 42 || object.ETag != "etag-list" ||
		object.StorageClass != "Standard" || !object.LastModified.Equal(lastModified) {
		t.Fatalf("listed object = %+v", object)
	}
	if len(page.CommonPrefixes) != 1 || page.CommonPrefixes[0] != "reports/archive/" ||
		page.NextCursor != "cursor-next" || !page.Truncated {
		t.Fatalf("list page = %+v", page)
	}
}

func TestListObjectsHandlesEmptyPageAndRejectsInvalidResponses(t *testing.T) {
	t.Run("empty page", func(t *testing.T) {
		client := &recordingObjectClient{listResult: &aliyunoss.ListObjectsV2Result{}}
		page, err := bucketWithClient(client).ListObjects(context.Background(), foundationoss.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		request := client.listRequest
		if request == nil || request.Prefix != nil || request.Delimiter != nil ||
			request.ContinuationToken != nil || request.MaxKeys != 0 {
			t.Fatalf("zero-options request = %#v", request)
		}
		if page.Objects == nil || page.CommonPrefixes == nil || len(page.Objects) != 0 ||
			len(page.CommonPrefixes) != 0 || page.NextCursor != "" || page.Truncated {
			t.Fatalf("empty page = %#v", page)
		}
	})

	t.Run("negative max keys", func(t *testing.T) {
		client := &recordingObjectClient{}
		_, err := bucketWithClient(client).ListObjects(context.Background(), foundationoss.ListOptions{MaxKeys: -1})
		if err == nil || client.listRequest != nil {
			t.Fatalf("ListObjects(-1) error = %v, request = %#v", err, client.listRequest)
		}
	})

	t.Run("nil response", func(t *testing.T) {
		client := &recordingObjectClient{}
		if _, err := bucketWithClient(client).ListObjects(context.Background(), foundationoss.ListOptions{}); err == nil {
			t.Fatal("nil provider response was accepted")
		}
	})

	t.Run("provider error", func(t *testing.T) {
		providerErr := errors.New("list unavailable")
		client := &recordingObjectClient{listErr: providerErr}
		_, err := bucketWithClient(client).ListObjects(context.Background(), foundationoss.ListOptions{})
		if !errors.Is(err, providerErr) {
			t.Fatalf("ListObjects error = %v", err)
		}
	})
}

func TestCopyObjectMapsReplacementRequestAndResult(t *testing.T) {
	lastModified := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	metadata := map[string]string{"trace": "original"}
	client := &recordingObjectClient{
		copyResult: &aliyunoss.CopyObjectResult{
			ETag:         aliyunoss.Ptr("etag-copy"),
			LastModified: &lastModified,
		},
	}
	bucket := bucketWithClient(client)

	info, err := bucket.CopyObject(
		context.Background(),
		" /source/object.txt ",
		" /destination/object.txt ",
		foundationoss.CopyOptions{
			Metadata:        metadata,
			ReplaceMetadata: true,
			ForbidOverwrite: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := client.copyRequest
	if request == nil {
		t.Fatal("CopyObject did not call the provider")
	}
	if got := aliyunoss.ToString(request.Bucket); got != "test-bucket" {
		t.Fatalf("request bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.Key); got != "destination/object.txt" {
		t.Fatalf("request destination key = %q", got)
	}
	if got := aliyunoss.ToString(request.SourceBucket); got != "test-bucket" {
		t.Fatalf("request source bucket = %q", got)
	}
	if got := aliyunoss.ToString(request.SourceKey); got != "source/object.txt" {
		t.Fatalf("request source key = %q", got)
	}
	if got := aliyunoss.ToString(request.ForbidOverwrite); got != "true" {
		t.Fatalf("request forbid overwrite = %q", got)
	}
	if got := aliyunoss.ToString(request.MetadataDirective); got != "REPLACE" {
		t.Fatalf("request metadata directive = %q", got)
	}
	if info.Key != "destination/object.txt" || info.ETag != "etag-copy" ||
		!info.LastModified.Equal(lastModified) {
		t.Fatalf("CopyObject info = %+v", info)
	}
	metadata["trace"] = "changed"
	if !maps.Equal(request.Metadata, map[string]string{"trace": "original"}) ||
		!maps.Equal(info.Metadata, map[string]string{"trace": "original"}) {
		t.Fatalf("metadata was not detached: request=%v info=%v", request.Metadata, info.Metadata)
	}
}

func TestCopyObjectCopiesSourceMetadataUnlessReplacementRequested(t *testing.T) {
	client := &recordingObjectClient{
		copyResult: &aliyunoss.CopyObjectResult{ETag: aliyunoss.Ptr("etag-copy")},
	}
	bucket := bucketWithClient(client)

	info, err := bucket.CopyObject(
		context.Background(),
		"source",
		"destination",
		foundationoss.CopyOptions{Metadata: map[string]string{"not-sent": "value"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if client.copyRequest.MetadataDirective != nil || client.copyRequest.Metadata != nil ||
		client.copyRequest.ForbidOverwrite != nil {
		t.Fatalf("default copy request unexpectedly replaces metadata: %#v", client.copyRequest)
	}
	if info.Metadata != nil {
		t.Fatalf("CopyObject reported metadata it did not send: %v", info.Metadata)
	}
}

func TestCopyObjectRejectsInvalidOrMissingResponsesAndMapsProviderConflicts(t *testing.T) {
	for _, test := range []struct {
		name        string
		source      string
		destination string
	}{
		{name: "empty source", destination: "destination"},
		{name: "empty destination", source: "source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &recordingObjectClient{}
			_, err := bucketWithClient(client).CopyObject(
				context.Background(), test.source, test.destination, foundationoss.CopyOptions{},
			)
			if err == nil || client.copyRequest != nil {
				t.Fatalf("CopyObject(%q, %q) error = %v, request = %#v", test.source, test.destination, err, client.copyRequest)
			}
		})
	}

	t.Run("nil response", func(t *testing.T) {
		client := &recordingObjectClient{}
		_, err := bucketWithClient(client).CopyObject(
			context.Background(), "source", "destination", foundationoss.CopyOptions{},
		)
		if err == nil {
			t.Fatal("nil provider response was accepted")
		}
	})

	t.Run("provider precondition", func(t *testing.T) {
		providerErr := &aliyunoss.ServiceError{StatusCode: http.StatusPreconditionFailed, Code: "PreconditionFailed"}
		client := &recordingObjectClient{copyErr: providerErr}
		_, err := bucketWithClient(client).CopyObject(
			context.Background(), "source", "destination", foundationoss.CopyOptions{},
		)
		if !errors.Is(err, foundationoss.ErrObjectAlreadyExists) || !errors.Is(err, providerErr) {
			t.Fatalf("CopyObject precondition error = %v", err)
		}
	})
}
