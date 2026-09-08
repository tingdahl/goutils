package storage

import (
	"bytes"
	"testing"
)

func TestBrotliCompressionRoundTrip(t *testing.T) {
	testData := []byte("Hello, world! This is a test string for Brotli compression and decompression in goutils.")

	compressed, err := CompressBrotli(testData)
	if err != nil {
		t.Fatalf("CompressBrotli failed: %v", err)
	}

	decompressed, err := DecompressBrotli(compressed)
	if err != nil {
		t.Fatalf("DecompressBrotli failed: %v", err)
	}

	if !bytes.Equal(decompressed, testData) {
		t.Fatalf("Decompressed data mismatch: expected %q, got %q", string(testData), string(decompressed))
	}
}

func TestBrotliEmpty(t *testing.T) {
	compressed, err := CompressBrotli(nil)
	if err != nil {
		t.Fatalf("CompressBrotli(nil) failed: %v", err)
	}
	if len(compressed) != 0 {
		t.Fatalf("Expected empty result, got %v", compressed)
	}

	decompressed, err := DecompressBrotli(nil)
	if err != nil {
		t.Fatalf("DecompressBrotli(nil) failed: %v", err)
	}
	if len(decompressed) != 0 {
		t.Fatalf("Expected empty result, got %v", decompressed)
	}

	// Also test empty non-nil slice
	c2, err := CompressBrotli([]byte{})
	if err != nil || len(c2) != 0 {
		t.Fatalf("Expected empty result for []byte{}, got %v, err: %v", c2, err)
	}
	d2, err := DecompressBrotli([]byte{})
	if err != nil || len(d2) != 0 {
		t.Fatalf("Expected empty result for []byte{}, got %v, err: %v", d2, err)
	}
}

func TestBrotliCorrupted(t *testing.T) {
	corrupted := []byte{0x01, 0x02, 0x03, 0x04}
	_, err := DecompressBrotli(corrupted)
	if err == nil {
		t.Fatal("Expected error for corrupted data, got nil")
	}
}
