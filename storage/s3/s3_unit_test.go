//go:build !exclude_s3

package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"strconv"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tingdahl/goutils/storage"
)

func TestMinimalS3Client_WithMockServer(t *testing.T) {
	objects := make(map[string][]byte)
	headersMap := make(map[string]http.Header)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify SigV4 Authorization header or query param
		auth := r.Header.Get("Authorization")
		if auth == "" && !strings.Contains(r.URL.RawQuery, "X-Amz-Signature") {
			t.Errorf("Request missing SigV4 Authorization or signature: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if auth != "" && r.Header.Get("X-Amz-Content-Sha256") == "" {
			t.Errorf("Request missing required X-Amz-Content-Sha256 header: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")

		switch r.Method {
		case http.MethodHead:
			data, ok := objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", `"mock-etag-1"`)
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)

		case http.MethodPut:
			ifMatch := r.Header.Get("If-Match")
			if ifMatch != "" && ifMatch != `"mock-etag-1"` {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(r.Body)
			objects[key] = buf.Bytes()
			headersMap[key] = r.Header.Clone()
			w.Header().Set("ETag", `"mock-etag-1"`)
			w.WriteHeader(http.StatusOK)

		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				prefix := r.URL.Query().Get("prefix")
				delimiter := r.URL.Query().Get("delimiter")
				res := listBucketResultXML{
					IsTruncated: false,
				}
				for k := range objects {
					if strings.HasPrefix(k, prefix) {
						if delimiter != "" && strings.Contains(strings.TrimPrefix(k, prefix), delimiter) {
							res.CommonPrefixes = append(res.CommonPrefixes, struct {
								Prefix string `xml:"Prefix"`
							}{Prefix: prefix + "sub/"})
						} else {
							res.Contents = append(res.Contents, struct {
								Key          string `xml:"Key"`
								LastModified string `xml:"LastModified"`
							}{Key: k, LastModified: time.Now().Format(time.RFC3339)})
						}
					}
				}
				w.Header().Set("Content-Type", "application/xml")
				_ = xml.NewEncoder(w).Encode(res)
				return
			}

			data, ok := objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", `"mock-etag-1"`)
			w.Write(data)

		case http.MethodDelete:
			delete(objects, key)
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	client, err := NewS3StorageClient(map[string]string{
		EnvS3Bucket:           "test-bucket",
		EnvS3Region:           "nl-ams",
		EnvS3Endpoint:         server.URL,
		EnvAWSAccessKeyID:     "SCWTESTKEY",
		EnvAWSSecretAccessKey: "SCWTESTSECRET",
	})
	if err != nil {
		t.Fatalf("NewS3StorageClient failed: %v", err)
	}

	ctx := context.Background()

	// 1. WriteObject (.zst file)
	origPayload := []byte("Testing Zstandard S3 compression with minimal HTTP client")
	etag, err := client.WriteObject(ctx, "data/doc.pb.zst", origPayload)
	if err != nil {
		t.Fatalf("WriteObject failed: %v", err)
	}
	if etag != "mock-etag-1" {
		t.Errorf("Expected ETag mock-etag-1, got %s", etag)
	}

	// Verify Content-Encoding: zstd was sent
	h := headersMap["data/doc.pb.zst"]
	if h.Get("Content-Encoding") != "zstd" {
		t.Errorf("Expected Content-Encoding: zstd, got %s", h.Get("Content-Encoding"))
	}

	// 2. ReadObject (.zst file should automatically decompress)
	readData, readEtag, err := client.ReadObject(ctx, "data/doc.pb.zst")
	if err != nil {
		t.Fatalf("ReadObject failed: %v", err)
	}
	if readEtag != "mock-etag-1" {
		t.Errorf("Expected ETag mock-etag-1, got %s", readEtag)
	}
	if string(readData) != string(origPayload) {
		t.Errorf("Decompressed content mismatch: got %s, want %s", string(readData), string(origPayload))
	}

	// 3. GetCurrentRevision
	rev, err := client.GetCurrentRevision(ctx, "data/doc.pb.zst")
	if err != nil {
		t.Fatalf("GetCurrentRevision failed: %v", err)
	}
	if rev != "mock-etag-1" {
		t.Errorf("Expected rev mock-etag-1, got %s", rev)
	}

	// 4. WriteObjectIfRevisionMatch (failure)
	_, err = client.WriteObjectIfRevisionMatch(ctx, "data/doc.pb.zst", []byte("bad"), "wrong-rev")
	if !errors.Is(err, storage.RevisionWriteError) && err != storage.RevisionWriteError {
		t.Fatalf("Expected RevisionWriteError on mismatch, got: %v", err)
	}

	// 5. WriteObjectIfRevisionMatch (success)
	newRev, err := client.WriteObjectIfRevisionMatch(ctx, "data/doc.pb.zst", []byte("updated"), "mock-etag-1")
	if err != nil {
		t.Fatalf("WriteObjectIfRevisionMatch failed: %v", err)
	}
	if newRev != "mock-etag-1" {
		t.Errorf("Expected revision mock-etag-1, got %s", newRev)
	}

	// 6. GetObjectLink (presigned GET with response-content-encoding=zstd)
	link, err := client.GetObjectLink(ctx, "data/doc.pb.zst", 300, "")
	if err != nil {
		t.Fatalf("GetObjectLink failed: %v", err)
	}
	if !strings.Contains(link, "response-content-encoding=zstd") {
		t.Errorf("Presigned URL missing response-content-encoding=zstd: %s", link)
	}
	if !strings.Contains(link, "X-Amz-Signature") {
		t.Errorf("Presigned URL missing X-Amz-Signature: %s", link)
	}

	// 7. GetUploadLink (presigned PUT)
	upLink, err := client.GetUploadLink(ctx, "data/upload.png", 300, "image/png")
	if err != nil {
		t.Fatalf("GetUploadLink failed: %v", err)
	}
	if !strings.Contains(upLink, "X-Amz-Signature") {
		t.Errorf("Upload URL missing signature: %s", upLink)
	}

	// 8. ListObjects
	objs, err := client.ListObjects(ctx, "data/")
	if err != nil {
		t.Fatalf("ListObjects failed: %v", err)
	}
	if len(objs) == 0 {
		t.Error("Expected listed objects, got 0")
	}

	// 9. DeleteObject
	if err := client.DeleteObject(ctx, "data/doc.pb.zst"); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
}
