package goutils

import (
	"testing"
)

func TestRandomID(t *testing.T) {
	seen := make(map[int64]bool)
	const count = 1000

	for i := 0; i < count; i++ {
		id, err := RandomID()
		if err != nil {
			t.Fatalf("RandomID failed on iteration %d: %v", i, err)
		}
		if id < 1 {
			t.Fatalf("RandomID generated value < 1: %d", id)
		}
		if id > TypeScriptMaxSafeInteger {
			t.Fatalf("RandomID generated value > TypeScriptMaxSafeInteger: %d", id)
		}
		if seen[id] {
			t.Fatalf("RandomID collision detected: %d", id)
		}
		seen[id] = true
	}
}
