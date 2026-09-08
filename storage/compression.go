package storage

import (
	brrr "github.com/molecule-man/go-brrr"
)

const defaultBrotliQuality = 4

// CompressBrotli compresses the input data using Brotli with quality level 4 (optimal for on-the-fly writes).
func CompressBrotli(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	return brrr.Compress(data, defaultBrotliQuality)
}

// DecompressBrotli decompresses the Brotli-compressed input data.
func DecompressBrotli(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	return brrr.Decompress(data)
}
