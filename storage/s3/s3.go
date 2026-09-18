//go:build !exclude_s3

package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/tingdahl/goutils/config"
	"github.com/tingdahl/goutils/storage"
)

const (
	EnvS3Bucket           = "S3_BUCKET"
	EnvS3Region           = "S3_REGION"
	EnvS3Endpoint         = "S3_ENDPOINT"
	EnvAWSAccessKeyID     = "AWS_ACCESS_KEY_ID"
	EnvAWSSecretAccessKey = "AWS_SECRET_ACCESS_KEY"

	// MaxReadObjectSizeBytes limits the maximum object payload read into memory to prevent OOM (100 MB).
	MaxReadObjectSizeBytes int64 = 100 << 20
)

var ErrObjectTooLarge = errors.New("storage: object exceeds maximum permitted read size (100 MB)")

func getVal(opts map[string]string, key string) string {
	if opts != nil {
		if v, ok := opts[key]; ok && v != "" {
			return v
		}
	}
	return config.GetConfigString(key)
}

// Init registers NewS3StorageClient as the constructor in storage.
func Init() error {
	storage.SetStorageConstructor(NewS3StorageClient)
	return nil
}

// S3StorageClient implements storage.StorageClient using a minimal HTTP client and AWS SigV4 signer.
type S3StorageClient struct {
	httpClient *http.Client
	signer     *v4.Signer
	creds      aws.Credentials
	bucket     string
	region     string
	endpoint   string
}

// NewS3StorageClient creates a new S3 client configured from the provided options or environment.
func NewS3StorageClient(opts map[string]string) (storage.StorageClient, error) {
	bucket := getVal(opts, EnvS3Bucket)
	if bucket == "" {
		return nil, errors.New("s3: S3_BUCKET is required but not set")
	}

	region := getVal(opts, EnvS3Region)
	if region == "" {
		region = "us-east-1"
	}

	endpoint := getVal(opts, EnvS3Endpoint)
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	} else {
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			endpoint = "https://" + endpoint
		}
		endpoint = strings.TrimRight(endpoint, "/")
	}

	accessKey := getVal(opts, EnvAWSAccessKeyID)
	secretKey := getVal(opts, EnvAWSSecretAccessKey)
	var credVal aws.Credentials
	if accessKey != "" && secretKey != "" {
		p := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
		var err error
		credVal, err = p.Retrieve(context.Background())
		if err != nil {
			return nil, fmt.Errorf("s3: credentials retrieval failed: %w", err)
		}
	}

	return &S3StorageClient{
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		signer:   v4.NewSigner(),
		creds:    credVal,
		bucket:   bucket,
		region:   region,
		endpoint: endpoint,
	}, nil
}

func (s *S3StorageClient) objectURL(object string) string {
	cleanObj := strings.TrimPrefix(object, "/")
	return fmt.Sprintf("%s/%s/%s", s.endpoint, s.bucket, cleanObj)
}

func (s *S3StorageClient) bucketURL() string {
	return fmt.Sprintf("%s/%s", s.endpoint, s.bucket)
}

func (s *S3StorageClient) signAndDo(ctx context.Context, method, targetURL string, body []byte, headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader
	payloadHash := hex.EncodeToString(sha256.New().Sum(nil))
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
		h := sha256.Sum256(body)
		payloadHash = hex.EncodeToString(h[:])
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if err := s.signer.SignHTTP(ctx, s.creds, req, payloadHash, "s3", s.region, time.Now()); err != nil {
		return nil, fmt.Errorf("s3: signing error: %w", err)
	}

	return s.httpClient.Do(req)
}

// GetCurrentRevision fetches the ETag of an object via HeadObject.
func (s *S3StorageClient) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	resp, err := s.signAndDo(ctx, http.MethodHead, s.objectURL(object), nil, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("s3: HeadObject %s failed: status %d", object, resp.StatusCode)
	}

	etag := strings.Trim(resp.Header.Get("ETag"), "\"")
	return etag, nil
}

func (s *S3StorageClient) putObject(ctx context.Context, file string, data []byte, contentType, contentEncoding, ifMatch, ifNoneMatch string) (string, error) {
	headers := make(map[string]string)
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	if contentEncoding != "" {
		headers["Content-Encoding"] = contentEncoding
	}
	if ifMatch != "" {
		if !strings.HasPrefix(ifMatch, "\"") && ifMatch != "*" {
			ifMatch = fmt.Sprintf("\"%s\"", ifMatch)
		}
		headers["If-Match"] = ifMatch
	}
	if ifNoneMatch != "" {
		headers["If-None-Match"] = ifNoneMatch
	}

	resp, err := s.signAndDo(ctx, http.MethodPut, s.objectURL(file), data, headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPreconditionFailed {
		return "", storage.RevisionWriteError
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("s3: PutObject %s failed: status %d: %s", file, resp.StatusCode, string(b))
	}

	etag := strings.Trim(resp.Header.Get("ETag"), "\"")
	return etag, nil
}

// WriteObject writes data to S3, compressing with Zstandard if key ends with .zst.
func (s *S3StorageClient) WriteObject(ctx context.Context, file string, data []byte) (string, error) {
	contentType := storage.ContentTypeFromKey(file)
	var contentEncoding string
	payload := data

	if storage.IsZstdKey(file) {
		compressed, err := storage.CompressZstd(data)
		if err != nil {
			return "", fmt.Errorf("failed to compress zstd payload for %s: %w", file, err)
		}
		payload = compressed
		contentEncoding = "zstd"
	}

	return s.putObject(ctx, file, payload, contentType, contentEncoding, "", "")
}

// WriteRawObject writes raw data directly to S3 without compression.
func (s *S3StorageClient) WriteRawObject(ctx context.Context, file string, data []byte) (string, error) {
	contentType := storage.ContentTypeFromKey(file)
	var contentEncoding string
	if storage.IsZstdKey(file) {
		contentEncoding = "zstd"
	}
	return s.putObject(ctx, file, data, contentType, contentEncoding, "", "")
}

// WriteObjectIfRevisionMatch writes conditionally based on ETag match.
func (s *S3StorageClient) WriteObjectIfRevisionMatch(ctx context.Context, file string, data []byte, revision string) (string, error) {
	contentType := storage.ContentTypeFromKey(file)
	var contentEncoding string
	payload := data

	if storage.IsZstdKey(file) {
		compressed, err := storage.CompressZstd(data)
		if err != nil {
			return "", fmt.Errorf("failed to compress zstd payload for %s: %w", file, err)
		}
		payload = compressed
		contentEncoding = "zstd"
	}

	if revision == "" {
		return s.putObject(ctx, file, payload, contentType, contentEncoding, "", "*")
	}

	headRev, err := s.GetCurrentRevision(ctx, file)
	if err != nil {
		return "", err
	}
	if headRev == "" || headRev != revision {
		return "", storage.RevisionWriteError
	}

	return s.putObject(ctx, file, payload, contentType, contentEncoding, revision, "")
}

// ReadRawObject reads object data without Zstandard decompression.
func (s *S3StorageClient) ReadRawObject(ctx context.Context, file string) ([]byte, string, error) {
	resp, err := s.signAndDo(ctx, http.MethodGet, s.objectURL(file), nil, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("s3: object %s not found", file)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("s3: GetObject %s failed: status %d", file, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxReadObjectSizeBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > MaxReadObjectSizeBytes {
		return nil, "", ErrObjectTooLarge
	}

	etag := strings.Trim(resp.Header.Get("ETag"), "\"")
	return data, etag, nil
}

// ReadObject reads object data, automatically decompressing Zstandard if key ends with .zst.
func (s *S3StorageClient) ReadObject(ctx context.Context, file string) ([]byte, string, error) {
	data, etag, err := s.ReadRawObject(ctx, file)
	if err != nil {
		return nil, "", err
	}

	if storage.IsZstdKey(file) {
		decompressed, err := storage.DecompressZstd(data)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decompress zstd payload for %s: %w", file, err)
		}
		data = decompressed
	}

	return data, etag, nil
}

// GetObjectLink generates a presigned GET URL with response-content-encoding=zstd for .zst files.
func (s *S3StorageClient) GetObjectLink(ctx context.Context, object string, duration int, IPAddress string) (string, error) {
	targetURL := s.objectURL(object)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}

	q := req.URL.Query()
	q.Set("X-Amz-Expires", strconv.Itoa(duration))
	if storage.IsZstdKey(object) {
		q.Set("response-content-encoding", "zstd")
		q.Set("response-content-type", storage.ContentTypeFromKey(object))
	}
	req.URL.RawQuery = q.Encode()

	signedURL, _, err := s.signer.PresignHTTP(ctx, s.creds, req, "UNSIGNED-PAYLOAD", "s3", s.region, time.Now())
	if err != nil {
		return "", fmt.Errorf("s3: presign get failed: %w", err)
	}

	return signedURL, nil
}

// GetUploadLink generates a presigned PUT URL.
func (s *S3StorageClient) GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error) {
	targetURL := s.objectURL(object)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, nil)
	if err != nil {
		return "", err
	}

	q := req.URL.Query()
	q.Set("X-Amz-Expires", strconv.Itoa(duration))
	req.URL.RawQuery = q.Encode()

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	signedURL, _, err := s.signer.PresignHTTP(ctx, s.creds, req, "UNSIGNED-PAYLOAD", "s3", s.region, time.Now())
	if err != nil {
		return "", fmt.Errorf("s3: presign put failed: %w", err)
	}

	return signedURL, nil
}

// DeleteObject removes an object from S3.
func (s *S3StorageClient) DeleteObject(ctx context.Context, object string) error {
	resp, err := s.signAndDo(ctx, http.MethodDelete, s.objectURL(object), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("s3: DeleteObject %s failed: status %d", object, resp.StatusCode)
}

type listBucketResultXML struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	Contents              []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (s *S3StorageClient) listPage(ctx context.Context, prefix, delimiter, continuationToken string) (*listBucketResultXML, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	if continuationToken != "" {
		q.Set("continuation-token", continuationToken)
	}

	targetURL := fmt.Sprintf("%s?%s", s.bucketURL(), q.Encode())
	resp, err := s.signAndDo(ctx, http.MethodGet, targetURL, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("s3: ListObjects failed: status %d: %s", resp.StatusCode, string(b))
	}

	var result listBucketResultXML
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("s3: failed to decode list objects XML: %w", err)
	}
	return &result, nil
}

// ListPrefixes lists directory-like common prefixes for a delimiter.
func (s *S3StorageClient) ListPrefixes(ctx context.Context, prefix string, delimiter string) ([]string, error) {
	var prefixes []string
	continuationToken := ""

	for {
		page, err := s.listPage(ctx, prefix, delimiter, continuationToken)
		if err != nil {
			return nil, err
		}

		for _, cp := range page.CommonPrefixes {
			if cp.Prefix != "" {
				prefixes = append(prefixes, cp.Prefix)
			}
		}

		if !page.IsTruncated || page.NextContinuationToken == "" {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	return prefixes, nil
}

// ListObjects lists objects matching a prefix.
func (s *S3StorageClient) ListObjects(ctx context.Context, prefix string) ([]storage.StorageObject, error) {
	var objects []storage.StorageObject
	continuationToken := ""

	for {
		page, err := s.listPage(ctx, prefix, "", continuationToken)
		if err != nil {
			return nil, err
		}

		for _, item := range page.Contents {
			var lastMod time.Time
			if item.LastModified != "" {
				if t, err := time.Parse(time.RFC3339, item.LastModified); err == nil {
					lastMod = t
				} else if t, err := time.Parse("2006-01-02T15:04:05.000Z", item.LastModified); err == nil {
					lastMod = t
				}
			}
			objects = append(objects, storage.StorageObject{
				Key:          item.Key,
				LastModified: lastMod,
			})
		}

		if !page.IsTruncated || page.NextContinuationToken == "" {
			break
		}
		continuationToken = page.NextContinuationToken
	}

	return objects, nil
}
