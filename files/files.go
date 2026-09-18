package files

//go:generate protoc --go_out=. --go_opt=paths=source_relative files.v1.proto

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tingdahl/goutils/storage"
	"github.com/tingdahl/goutils/storage/docstore"
	"google.golang.org/protobuf/proto"
)

const (
	// SchemaMinor is the current minor version of the files schema.
	SchemaMinor int32 = 0
)

var (
	// ErrFileNotFound indicates that a requested file was not found in the catalog.
	ErrFileNotFound = errors.New("file not found")

	filesInitOnce sync.Once
	filesRepo     *docstore.Repository[*FilesClient]
	filesStore    storage.StorageClient
)

// FilesCatalogObjectName returns the object key for the files catalog protobuf document.
func FilesCatalogObjectName(prefix string) string {
	cleanPrefix := strings.Trim(prefix, "/")
	if cleanPrefix == "" {
		return "files.v1.pb.zst"
	}
	return fmt.Sprintf("%s/files.v1.pb.zst", cleanPrefix)
}

// FileRawObjectName returns the object key for a raw file's binary content.
func FileRawObjectName(prefix string, fileId int32) string {
	cleanPrefix := strings.Trim(prefix, "/")
	if cleanPrefix == "" {
		return fmt.Sprintf("files/%d", fileId)
	}
	return fmt.Sprintf("%s/files/%d", cleanPrefix, fileId)
}

// Init initializes the files repository with the given storage client.
func Init(store storage.StorageClient, opts ...docstore.RepositoryOption) error {
	if store == nil {
		return errors.New("storage client cannot be nil")
	}

	filesInitOnce.Do(func() {
		filesStore = store
		filesRepo = docstore.NewRepository(filesClientFactory, opts...)
	})
	return nil
}

// ResetForTesting resets the package repository state for test isolation.
func ResetForTesting() {
	filesInitOnce = sync.Once{}
	filesRepo = nil
	filesStore = nil
}

// GetFilesClient retrieves the cached FilesClient for the given storage prefix.
func GetFilesClient(ctx context.Context, prefix string, opts ...docstore.GetOption) (*FilesClient, error) {
	if filesRepo == nil {
		return nil, errors.New("files package not initialized: call files.Init() first")
	}
	cleanPrefix := strings.Trim(prefix, "/")
	return filesRepo.Get(ctx, FilesCatalogObjectName(cleanPrefix), opts...)
}

// FilesClient manages a catalog of files and raw file binary storage within a prefix.
type FilesClient struct {
	docstore.Document
	data   FilesCatalogProto
	prefix string
}

func (f *FilesClient) Prefix() string {
	return f.prefix
}

func (f *FilesClient) ReadFromProto(data []byte) error {
	proto.Reset(&f.data)
	if data == nil {
		return nil
	}
	return proto.Unmarshal(data, &f.data)
}

func (f *FilesClient) GetSchemaMinorVersion() int32 {
	return f.data.SchemaMinorVersion
}

func (f *FilesClient) StampVersion(msg proto.Message, schemaMinor int32, commit string) {
	doc := msg.(*FilesCatalogProto)
	doc.SchemaMinorVersion = schemaMinor
	doc.LastModifiedByCommit = commit
}

func filesClientFactory(ctx context.Context, objectPath string) (*FilesClient, error) {
	if filesStore == nil {
		return nil, errors.New("storage client cannot be nil")
	}
	if objectPath == "" {
		return nil, errors.New("object path cannot be empty")
	}

	cleanPrefix := ""
	if idx := strings.LastIndex(objectPath, "/files.v1.pb.zst"); idx != -1 {
		cleanPrefix = objectPath[:idx]
	}

	fc := &FilesClient{prefix: cleanPrefix}
	fc.Document = docstore.Document{
		Client:               fc,
		Storage:              filesStore,
		ObjectName:           objectPath,
		ProtoMsg:             &fc.data,
		SupportedSchemaMinor: SchemaMinor,
	}

	if err := fc.DoLoad(); err != nil {
		return nil, fmt.Errorf("failed to load files catalog: %w", err)
	}

	return fc, nil
}

// GetFiles returns a slice of all files in the catalog.
func (f *FilesClient) GetFiles() []*FileMetadataProto {
	f.Rwlock.RLock()
	defer f.Rwlock.RUnlock()
	copied := make([]*FileMetadataProto, len(f.data.Files))
	for i, file := range f.data.Files {
		copied[i] = proto.Clone(file).(*FileMetadataProto)
	}
	return copied
}

// GetFile retrieves metadata for a specific file by file_id.
func (f *FilesClient) GetFile(fileId int32) *FileMetadataProto {
	f.Rwlock.RLock()
	defer f.Rwlock.RUnlock()
	for _, file := range f.data.Files {
		if file.FileId == fileId {
			return proto.Clone(file).(*FileMetadataProto)
		}
	}
	return nil
}

// GetFilesByReference returns all files matching a specific reference.
func (f *FilesClient) GetFilesByReference(refType ReferenceType, refId int32, entityId string) []*FileMetadataProto {
	f.Rwlock.RLock()
	defer f.Rwlock.RUnlock()
	var matched []*FileMetadataProto
	for _, file := range f.data.Files {
		if file.Reference == nil {
			continue
		}
		if file.Reference.Type != refType {
			continue
		}
		if refId != 0 && file.Reference.Id != refId {
			continue
		}
		if entityId != "" && file.Reference.EntityId != entityId {
			continue
		}
		matched = append(matched, proto.Clone(file).(*FileMetadataProto))
	}
	return matched
}

// WriteFileContent writes the raw binary content of a file to object storage.
func (f *FilesClient) WriteFileContent(ctx context.Context, fileId int32, data []byte) error {
	rawObj := FileRawObjectName(f.prefix, fileId)
	_, err := filesStore.WriteObject(ctx, rawObj, data)
	return err
}

// ReadFileContent reads the raw binary content of a file from object storage.
func (f *FilesClient) ReadFileContent(ctx context.Context, fileId int32) ([]byte, error) {
	rawObj := FileRawObjectName(f.prefix, fileId)
	data, _, err := filesStore.ReadRawObject(ctx, rawObj)
	return data, err
}

// GetSignedURL generates a presigned download URL for the file valid for durationSeconds.
// If durationSeconds <= 0, a default of 60 seconds is used.
func (f *FilesClient) GetSignedURL(ctx context.Context, fileId int32, durationSeconds int) (string, error) {
	if fileId <= 0 {
		return "", errors.New("invalid file id")
	}
	if filesStore == nil {
		return "", errors.New("storage client cannot be nil")
	}
	if durationSeconds <= 0 {
		durationSeconds = 60
	}
	rawObj := FileRawObjectName(f.prefix, fileId)
	return filesStore.GetObjectLink(ctx, rawObj, durationSeconds, "")
}

// AddFile registers a new file metadata record in the catalog and assigns a unique file_id.
func (f *FilesClient) AddFile(ctx context.Context, file *FileMetadataProto, userId string) (*FileMetadataProto, error) {
	var createdFile *FileMetadataProto
	err := f.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*FilesCatalogProto)
		now := time.Now().UnixMilli()

		file.CreatedAtUnixMs = now
		file.UpdatedAtUnixMs = now
		file.CreatedBy = userId

		if file.Reference == nil {
			file.Reference = &FileReferenceProto{
				Type: ReferenceType_UNASSIGNED,
			}
		}

		var maxId int32 = 0
		for _, item := range doc.Files {
			if item.FileId > maxId {
				maxId = item.FileId
			}
		}
		file.FileId = maxId + 1

		doc.Files = append(doc.Files, file)
		createdFile = proto.Clone(file).(*FileMetadataProto)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return createdFile, nil
}

// SaveFile convenience function to register metadata and store raw content in one operation.
func (f *FilesClient) SaveFile(ctx context.Context, name string, mimeType string, data []byte, ref *FileReferenceProto, userId string) (*FileMetadataProto, error) {
	meta := &FileMetadataProto{
		Name:      name,
		MimeType:  mimeType,
		SizeBytes: int64(len(data)),
		Reference: ref,
	}

	created, err := f.AddFile(ctx, meta, userId)
	if err != nil {
		return nil, err
	}

	if err := f.WriteFileContent(ctx, created.FileId, data); err != nil {
		// Roll back metadata if raw write fails
		_ = f.DeleteFile(ctx, created.FileId)
		return nil, fmt.Errorf("failed to write raw file content: %w", err)
	}

	return created, nil
}

// SetReference updates the target entity reference on an existing file record.
func (f *FilesClient) SetReference(ctx context.Context, fileId int32, ref *FileReferenceProto) error {
	return f.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*FilesCatalogProto)
		now := time.Now().UnixMilli()

		for _, file := range doc.Files {
			if file.FileId == fileId {
				file.Reference = ref
				file.UpdatedAtUnixMs = now
				return nil
			}
		}
		return ErrFileNotFound
	})
}

// DeleteFile removes the file metadata record and deletes the raw binary object from storage.
func (f *FilesClient) DeleteFile(ctx context.Context, fileId int32) error {
	var found bool
	err := f.Update(ctx, func(msg proto.Message) error {
		doc := msg.(*FilesCatalogProto)
		var updated []*FileMetadataProto
		for _, file := range doc.Files {
			if file.FileId == fileId {
				found = true
			} else {
				updated = append(updated, file)
			}
		}
		if !found {
			return ErrFileNotFound
		}
		doc.Files = updated
		return nil
	})
	if err != nil {
		return err
	}

	rawObj := FileRawObjectName(f.prefix, fileId)
	_ = filesStore.DeleteObject(ctx, rawObj)
	return nil
}
