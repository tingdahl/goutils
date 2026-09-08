//go:build !exclude_gcs

package gcs

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/tingdahl/goutils/storage"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
)

// GoogleStorageClient implements storage.StorageClient using Google Cloud Storage SDK.
type GoogleStorageClient struct {
	client *gcs.Client
}

// Init configures the storage package constructor to instantiate a GoogleStorageClient.
func Init() error {
	storage.SetStorageConstructor(func() (storage.StorageClient, error) {
		return NewGoogleStorageClient(context.Background())
	})
	return nil
}

// NewGoogleStorageClient creates a new GCS client.
func NewGoogleStorageClient(ctx context.Context) (storage.StorageClient, error) {
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}
	return &GoogleStorageClient{client: client}, nil
}

// GetCurrentRevision retrieves the object generation from GCS.
func (g *GoogleStorageClient) GetCurrentRevision(ctx context.Context, bucket string, object string) (string, error) {
	objectHandle := g.client.Bucket(bucket).Object(object)
	attrs, err := objectHandle.Attrs(ctx)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(attrs.Generation, 10), nil
}

func prepareGCSWriter(writer *gcs.Writer, file string, data []byte) ([]byte, error) {
	writer.ObjectAttrs.ContentType = storage.ContentTypeFromKey(file)
	if storage.IsBrotliKey(file) {
		compressed, err := storage.CompressBrotli(data)
		if err != nil {
			return nil, fmt.Errorf("failed to compress brotli payload for %s: %w", file, err)
		}
		writer.ObjectAttrs.ContentEncoding = "br"
		return compressed, nil
	}
	return data, nil
}

// WriteObject writes data to GCS, compressing with Brotli if key ends with .br.
func (g *GoogleStorageClient) WriteObject(ctx context.Context, bucket string, file string, data []byte) (string, error) {
	object := g.client.Bucket(bucket).Object(file)
	writer := object.NewWriter(ctx)
	payload, err := prepareGCSWriter(writer, file, data)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(payload); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return strconv.FormatInt(writer.Attrs().Generation, 10), nil
}

// WriteRawObject writes raw data directly to GCS without compression.
func (g *GoogleStorageClient) WriteRawObject(ctx context.Context, bucket string, file string, data []byte) (string, error) {
	object := g.client.Bucket(bucket).Object(file)
	writer := object.NewWriter(ctx)
	writer.ContentType = storage.ContentTypeFromKey(file)
	if storage.IsBrotliKey(file) {
		writer.ContentEncoding = "br"
	}
	if _, err := writer.Write(data); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return strconv.FormatInt(writer.Attrs().Generation, 10), nil
}

// WriteObjectIfRevisionMatch writes data to GCS conditionally based on generation match.
func (g *GoogleStorageClient) WriteObjectIfRevisionMatch(ctx context.Context, bucket string, file string, data []byte, revision string) (string, error) {
	object := g.client.Bucket(bucket).Object(file)
	var cond gcs.Conditions
	var gen int64
	var err error
	if revision != "" {
		gen, err = strconv.ParseInt(revision, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid revision: %w", err)
		}
	}

	if gen > 0 {
		cond = gcs.Conditions{GenerationMatch: gen}
	} else {
		cond = gcs.Conditions{DoesNotExist: true}
	}

	writer := object.If(cond).NewWriter(ctx)
	payload, err := prepareGCSWriter(writer, file, data)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(payload); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		if e, ok := err.(*googleapi.Error); ok && e.Code == 412 {
			return "", storage.RevisionWriteError
		}
		return "", err
	}
	return strconv.FormatInt(writer.Attrs().Generation, 10), nil
}

// ReadRawObject reads object data without decompression.
func (g *GoogleStorageClient) ReadRawObject(ctx context.Context, bucket string, file string) ([]byte, string, error) {
	object := g.client.Bucket(bucket).Object(file)
	reader, err := object.NewReader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}

	return data, strconv.FormatInt(reader.Attrs.Generation, 10), nil
}

// ReadObject reads object data, automatically decompressing Brotli if key ends with .br.
func (g *GoogleStorageClient) ReadObject(ctx context.Context, bucket string, file string) ([]byte, string, error) {
	data, gen, err := g.ReadRawObject(ctx, bucket, file)
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

	return data, gen, nil
}

// GetObjectLink generates a signed GET URL.
func (g *GoogleStorageClient) GetObjectLink(ctx context.Context, bucket string, object string, duration int, IPAddress string) (string, error) {
	bucketHandle := g.client.Bucket(bucket)
	opts := &gcs.SignedURLOptions{
		Scheme:  gcs.SigningSchemeV4,
		Method:  "GET",
		Expires: time.Now().Add(time.Duration(duration) * time.Second),
	}

	return bucketHandle.SignedURL(object, opts)
}

// GetUploadLink generates a signed PUT URL.
func (g *GoogleStorageClient) GetUploadLink(ctx context.Context, bucket string, object string, duration int, contentType string) (string, error) {
	bucketHandle := g.client.Bucket(bucket)
	opts := &gcs.SignedURLOptions{
		Scheme:      gcs.SigningSchemeV4,
		Method:      "PUT",
		ContentType: contentType,
		Expires:     time.Now().Add(time.Duration(duration) * time.Second),
	}

	return bucketHandle.SignedURL(object, opts)
}

// DeleteObject deletes an object from GCS.
func (g *GoogleStorageClient) DeleteObject(ctx context.Context, bucket string, object string) error {
	objectHandle := g.client.Bucket(bucket).Object(object)
	return objectHandle.Delete(ctx)
}

// ListPrefixes lists directory-like common prefixes for a delimiter.
func (g *GoogleStorageClient) ListPrefixes(ctx context.Context, bucket string, prefix string, delimiter string) ([]string, error) {
	var prefixes []string
	it := g.client.Bucket(bucket).Objects(ctx, &gcs.Query{
		Prefix:    prefix,
		Delimiter: delimiter,
	})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			prefixes = append(prefixes, attrs.Prefix)
		}
	}
	return prefixes, nil
}

// ListObjects lists objects matching a prefix.
func (g *GoogleStorageClient) ListObjects(ctx context.Context, bucket string, prefix string) ([]storage.StorageObject, error) {
	var objects []storage.StorageObject
	it := g.client.Bucket(bucket).Objects(ctx, &gcs.Query{
		Prefix: prefix,
	})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Name != "" {
			objects = append(objects, storage.StorageObject{
				Key:          attrs.Name,
				LastModified: attrs.Updated,
			})
		}
	}
	return objects, nil
}
