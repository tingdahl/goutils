package storage

import (
	"bytes"
	"testing"
)

func TestZstdCompressionRoundTrip(t *testing.T) {
	testData := []byte("Hello, world! This is a test string for Zstandard compression and decompression in goutils.")

	compressed, err := CompressZstd(testData)
	if err != nil {
		t.Fatalf("CompressZstd failed: %v", err)
	}

	decompressed, err := DecompressZstd(compressed)
	if err != nil {
		t.Fatalf("DecompressZstd failed: %v", err)
	}

	if !bytes.Equal(decompressed, testData) {
		t.Fatalf("Decompressed data mismatch: expected %q, got %q", string(testData), string(decompressed))
	}
}

func TestZstdEmpty(t *testing.T) {
	compressed, err := CompressZstd(nil)
	if err != nil {
		t.Fatalf("CompressZstd(nil) failed: %v", err)
	}
	if len(compressed) != 0 {
		t.Fatalf("Expected empty result, got %v", compressed)
	}

	decompressed, err := DecompressZstd(nil)
	if err != nil {
		t.Fatalf("DecompressZstd(nil) failed: %v", err)
	}
	if len(decompressed) != 0 {
		t.Fatalf("Expected empty result, got %v", decompressed)
	}

	c2, err := CompressZstd([]byte{})
	if err != nil || len(c2) != 0 {
		t.Fatalf("Expected empty result for []byte{}, got %v, err: %v", c2, err)
	}
	d2, err := DecompressZstd([]byte{})
	if err != nil || len(d2) != 0 {
		t.Fatalf("Expected empty result for []byte{}, got %v, err: %v", d2, err)
	}
}

func TestZstdCorrupted(t *testing.T) {
	corrupted := []byte{0x01, 0x02, 0x03, 0x04}
	_, err := DecompressZstd(corrupted)
	if err == nil {
		t.Fatal("Expected error for corrupted data, got nil")
	}
}
