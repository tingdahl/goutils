package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidToken indicates the token structure or signature is invalid.
	ErrInvalidToken = errors.New("invalid token")
	// ErrTokenExpired indicates the token has expired past its exp claim.
	ErrTokenExpired = errors.New("token expired")
	// ErrMissingSecret indicates the secret used for signing or verification is empty.
	ErrMissingSecret = errors.New("JWT secret is not configured")
)

const standardHeader = `{"alg":"HS256","typ":"JWT"}`

// Sign creates an HMAC-SHA256 signed JWT for the provided claims.
func Sign(claims any, secret string) (string, error) {
	if secret == "" {
		return "", ErrMissingSecret
	}

	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(standardHeader))

	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token claims: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	signatureB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signatureB64, nil
}

// Verify validates an HMAC-SHA256 JWT against the provided secret, checks expiration,
// and unmarshals the payload into claimsDst.
func Verify(tokenStr string, secret string, claimsDst any) error {
	if secret == "" {
		return ErrMissingSecret
	}

	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return ErrInvalidToken
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	actualSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expectedSig, actualSig) {
		return ErrInvalidToken
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}

	if claimsDst != nil {
		if err := json.Unmarshal(payloadBytes, claimsDst); err != nil {
			return ErrInvalidToken
		}
	}

	var expCheck struct {
		Exp int64 `json:"exp"`
	}
	_ = json.Unmarshal(payloadBytes, &expCheck)
	if expCheck.Exp > 0 && time.Now().Unix() > expCheck.Exp {
		return ErrTokenExpired
	}

	return nil
}

// ExtractClaimsUnverified unmarshals the token payload without signature verification.
// WARNING: This should only be used for debugging or non-security inspection.
func ExtractClaimsUnverified(tokenStr string, claimsDst any) error {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return ErrInvalidToken
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ErrInvalidToken
	}

	return json.Unmarshal(payloadBytes, claimsDst)
}
