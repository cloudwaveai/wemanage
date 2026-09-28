package blobstore

import (
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestBucketLookup(t *testing.T) {
	if bucketLookup("cos.ap-shanghai.myqcloud.com") != minio.BucketLookupDNS {
		t.Fatal("Tencent COS must use virtual-host style")
	}
	for _, ep := range []string{"oss-cn-shanghai.aliyuncs.com", "acct.r2.cloudflarestorage.com", "minio.local:9000"} {
		if bucketLookup(ep) != minio.BucketLookupAuto {
			t.Fatalf("%s should keep auto detection", ep)
		}
	}
}
