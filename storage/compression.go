package storage

import (
	"github.com/klauspost/compress/zstd"
)

var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	zstdDecoder, _ = zstd.NewReader(nil)
)

// CompressZstd compresses the input data using Zstandard with fastest level.
func CompressZstd(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	return zstdEncoder.EncodeAll(data, make([]byte, 0, len(data))), nil
}

// DecompressZstd decompresses the Zstandard-compressed input data.
func DecompressZstd(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	return zstdDecoder.DecodeAll(data, nil)
}

// CompressBrotli is an alias for CompressZstd for compatibility.
func CompressBrotli(data []byte) ([]byte, error) {
	return CompressZstd(data)
}

// DecompressBrotli is an alias for DecompressZstd for compatibility.
func DecompressBrotli(data []byte) ([]byte, error) {
	return DecompressZstd(data)
}
