//go:build !exclude_s3

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/tingdahl/goutils/config"
	"github.com/tingdahl/goutils/storage"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3StorageClient implements storage.StorageClient using AWS SDK v2.
type S3StorageClient struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
}

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

// NewS3StorageClient creates a new S3 client configured from the provided options or environment.
func NewS3StorageClient(opts map[string]string) (storage.StorageClient, error) {
	bucket := getVal(opts, EnvS3Bucket)
	if bucket == "" {
		return nil, errors.New("s3: S3_BUCKET is required but not set")
	}

	region := getVal(opts, EnvS3Region)
	endpoint := getVal(opts, EnvS3Endpoint)
	accessKey := getVal(opts, EnvAWSAccessKeyID)
	secretKey := getVal(opts, EnvAWSSecretAccessKey)

	ctx := context.Background()
	var loadOpts []func(*awsconfig.LoadOptions) error
	if region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))
	}
	if endpoint != "" {
		loadOpts = append(loadOpts, awsconfig.WithBaseEndpoint(endpoint))
	}
	if accessKey != "" && secretKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %w", err)
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	return &S3StorageClient{
		client:        client,
		presignClient: presignClient,
		bucket:        bucket,
	}, nil
}

// GetCurrentRevision fetches the ETag of an object via HeadObject.
func (s *S3StorageClient) GetCurrentRevision(ctx context.Context, object string) (string, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(object),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return "", nil
		}
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return *out.ETag, nil
}

func preparePutObjectInput(bucket string, file string, data []byte) (*s3.PutObjectInput, error) {
	contentType := storage.ContentTypeFromKey(file)
	var contentEncoding *string
	payload := data

	if storage.IsBrotliKey(file) {
		compressed, err := storage.CompressBrotli(data)
		if err != nil {
			return nil, fmt.Errorf("failed to compress brotli payload for %s: %w", file, err)
		}
		payload = compressed
		contentEncoding = aws.String("br")
	}

	input := &s3.PutObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(file),
		Body:              bytes.NewReader(payload),
		ContentType:       aws.String(contentType),
		ContentEncoding:   contentEncoding,
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	}
	return input, nil
}

// WriteObject writes data to S3, compressing with Brotli if key ends with .br.
func (s *S3StorageClient) WriteObject(ctx context.Context, file string, data []byte) (string, error) {
	putInput, err := preparePutObjectInput(s.bucket, file, data)
	if err != nil {
		return "", err
	}

	out, err := s.client.PutObject(ctx, putInput)
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return *out.ETag, nil
}

// WriteRawObject writes raw data directly to S3 without compression.
func (s *S3StorageClient) WriteRawObject(ctx context.Context, file string, data []byte) (string, error) {
	contentType := storage.ContentTypeFromKey(file)
	var contentEncoding *string
	if storage.IsBrotliKey(file) {
		contentEncoding = aws.String("br")
	}
	out, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(file),
		Body:              bytes.NewReader(data),
		ContentType:       aws.String(contentType),
		ContentEncoding:   contentEncoding,
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	})
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return *out.ETag, nil
}

// WriteObjectIfRevisionMatch writes conditionally based on ETag match.
func (s *S3StorageClient) WriteObjectIfRevisionMatch(ctx context.Context, file string, data []byte, revision string) (string, error) {
	putInput, err := preparePutObjectInput(s.bucket, file, data)
	if err != nil {
		return "", err
	}

	if revision == "" {
		putInput.IfNoneMatch = aws.String("*")
		out, err := s.client.PutObject(ctx, putInput)
		if err != nil {
			return "", err
		}
		if out.ETag == nil {
			return "", nil
		}
		return *out.ETag, nil
	}

	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(file),
	})
	if err != nil {
		return "", err
	}
	if head.ETag == nil || *head.ETag != revision {
		return "", storage.RevisionWriteError
	}

	putInput.IfMatch = aws.String(revision)
	out, err := s.client.PutObject(ctx, putInput)
	if err != nil {
		return "", err
	}
	if out.ETag == nil {
		return "", nil
	}
	return *out.ETag, nil
}

// ReadRawObject reads object data without Brotli decompression.
func (s *S3StorageClient) ReadRawObject(ctx context.Context, file string) ([]byte, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(file),
	})
	if err != nil {
		return nil, "", err
	}
	defer out.Body.Close()

	data, err := io.ReadAll(io.LimitReader(out.Body, MaxReadObjectSizeBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > MaxReadObjectSizeBytes {
		return nil, "", ErrObjectTooLarge
	}

	etag := ""
	if out.ETag != nil {
		etag = *out.ETag
	}

	return data, etag, nil
}

// ReadObject reads object data, automatically decompressing Brotli if key ends with .br.
func (s *S3StorageClient) ReadObject(ctx context.Context, file string) ([]byte, string, error) {
	data, etag, err := s.ReadRawObject(ctx, file)
	if err != nil {
		return nil, "", err
	}

	if storage.IsBrotliKey(file) {
		decompressed, err := storage.DecompressBrotli(data)
		if err != nil {
			return nil, "", fmt.Errorf("failed to decompress brotli payload for %s: %w", file, err)
		}
		data = decompressed
	}

	return data, etag, nil
}

// GetObjectLink generates a presigned GET URL.
func (s *S3StorageClient) GetObjectLink(ctx context.Context, object string, duration int, IPAddress string) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(object),
	}
	if storage.IsBrotliKey(object) {
		input.ResponseContentEncoding = aws.String("br")
		input.ResponseContentType = aws.String(storage.ContentTypeFromKey(object))
	}

	req, err := s.presignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(time.Duration(duration)*time.Second))
	if err != nil {
		return "", err
	}

	return req.URL, nil
}

// GetUploadLink generates a presigned PUT URL.
func (s *S3StorageClient) GetUploadLink(ctx context.Context, object string, duration int, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(object),
	}
	if contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	req, err := s.presignClient.PresignPutObject(ctx, input, s3.WithPresignExpires(time.Duration(duration)*time.Second))
	if err != nil {
		return "", err
	}

	return req.URL, nil
}

// DeleteObject removes an object from S3.
func (s *S3StorageClient) DeleteObject(ctx context.Context, object string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(object),
	})
	return err
}

// ListPrefixes lists directory-like common prefixes for a delimiter.
func (s *S3StorageClient) ListPrefixes(ctx context.Context, prefix string, delimiter string) ([]string, error) {
	var prefixes []string

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucket),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String(delimiter),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, commonPrefix := range page.CommonPrefixes {
			if commonPrefix.Prefix != nil {
				prefixes = append(prefixes, *commonPrefix.Prefix)
			}
		}
	}

	return prefixes, nil
}

// ListObjects lists objects matching a prefix.
func (s *S3StorageClient) ListObjects(ctx context.Context, prefix string) ([]storage.StorageObject, error) {
	var objects []storage.StorageObject

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, item := range page.Contents {
			if item.Key != nil {
				var lastMod time.Time
				if item.LastModified != nil {
					lastMod = *item.LastModified
				}
				objects = append(objects, storage.StorageObject{
					Key:          *item.Key,
					LastModified: lastMod,
				})
			}
		}
	}

	return objects, nil
}
