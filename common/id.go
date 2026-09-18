package common

import (
	crand "crypto/rand"
	"math/big"
)

// TypeScriptMaxSafeInteger is TypeScript/JavaScript Number.MAX_SAFE_INTEGER (2^53 - 1 = 9007199254740991).
// Values up to this limit can be safely represented as double-precision numbers without precision loss.
const TypeScriptMaxSafeInteger int64 = (1 << 53) - 1

// RandomID generates a cryptographically secure positive random int64 in the range [1, TypeScriptMaxSafeInteger].
// This guarantees full precision when serialized to JSON or parsed in TypeScript/JavaScript without requiring BigInt.
func RandomID() (int64, error) {
	max := big.NewInt(TypeScriptMaxSafeInteger)
	n, err := crand.Int(crand.Reader, max)
	if err != nil {
		return 0, err
	}
	return n.Int64() + 1, nil
}
