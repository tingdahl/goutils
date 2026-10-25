package jwt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/tingdahl/goutils/jwt"
)

type testClaims struct {
	Sub   string `json:"sub"`
	Role  string `json:"role"`
	Email string `json:"email"`
	Exp   int64  `json:"exp,omitempty"`
}

func TestSignAndVerify_Success(t *testing.T) {
	secret := "super-secure-secret-key-12345"
	claims := testClaims{
		Sub:   "user-123",
		Role:  "admin",
		Email: "test@example.com",
		Exp:   time.Now().Add(1 * time.Hour).Unix(),
	}

	token, err := jwt.Sign(claims, secret)
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	var verified testClaims
	if err := jwt.Verify(token, secret, &verified); err != nil {
		t.Fatalf("jwt.Verify failed: %v", err)
	}

	if verified.Sub != claims.Sub || verified.Role != claims.Role || verified.Email != claims.Email {
		t.Errorf("claims mismatch: got %+v, want %+v", verified, claims)
	}
}

func TestVerify_Expired(t *testing.T) {
	secret := "test-secret"
	claims := testClaims{
		Sub: "user-expired",
		Exp: time.Now().Add(-10 * time.Minute).Unix(),
	}

	token, err := jwt.Sign(claims, secret)
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}

	var dst testClaims
	err = jwt.Verify(token, secret, &dst)
	if !errors.Is(err, jwt.ErrTokenExpired) {
		t.Errorf("expected ErrTokenExpired, got: %v", err)
	}
}

func TestVerify_NoExp_Succeeds(t *testing.T) {
	secret := "test-secret"
	claims := testClaims{
		Sub: "user-no-exp",
	}

	token, err := jwt.Sign(claims, secret)
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}

	var dst testClaims
	if err := jwt.Verify(token, secret, &dst); err != nil {
		t.Fatalf("expected success for token without exp, got: %v", err)
	}
	if dst.Sub != "user-no-exp" {
		t.Errorf("expected sub 'user-no-exp', got %s", dst.Sub)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	token, err := jwt.Sign(testClaims{Sub: "user-1"}, "secret-a")
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}

	var dst testClaims
	err = jwt.Verify(token, "secret-b", &dst)
	if !errors.Is(err, jwt.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for wrong secret, got: %v", err)
	}
}

func TestVerify_Tampered(t *testing.T) {
	secret := "test-secret"
	token, err := jwt.Sign(testClaims{Sub: "user-1"}, secret)
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}

	// Tamper with middle segment
	tampered := token[:10] + "X" + token[11:]
	var dst testClaims
	err = jwt.Verify(tampered, secret, &dst)
	if !errors.Is(err, jwt.ErrInvalidToken) {
		t.Errorf("expected ErrInvalidToken for tampered token, got: %v", err)
	}
}

func TestVerify_Malformed(t *testing.T) {
	secret := "test-secret"
	var dst testClaims

	malformed := []string{
		"",
		"not-a-jwt",
		"header.payload",
		"header.payload.signature.extra",
		"not_base64.not_base64.not_base64",
	}

	for _, m := range malformed {
		if err := jwt.Verify(m, secret, &dst); !errors.Is(err, jwt.ErrInvalidToken) {
			t.Errorf("expected ErrInvalidToken for %q, got: %v", m, err)
		}
	}
}

func TestMissingSecret(t *testing.T) {
	_, err := jwt.Sign(testClaims{Sub: "u"}, "")
	if !errors.Is(err, jwt.ErrMissingSecret) {
		t.Errorf("expected ErrMissingSecret from Sign, got: %v", err)
	}

	err = jwt.Verify("a.b.c", "", nil)
	if !errors.Is(err, jwt.ErrMissingSecret) {
		t.Errorf("expected ErrMissingSecret from Verify, got: %v", err)
	}
}

func TestExtractClaimsUnverified(t *testing.T) {
	secret := "my-secret"
	claims := testClaims{Sub: "inspect-me", Role: "guest"}
	token, err := jwt.Sign(claims, secret)
	if err != nil {
		t.Fatalf("jwt.Sign failed: %v", err)
	}

	var dst testClaims
	if err := jwt.ExtractClaimsUnverified(token, &dst); err != nil {
		t.Fatalf("ExtractClaimsUnverified failed: %v", err)
	}
	if dst.Sub != "inspect-me" || dst.Role != "guest" {
		t.Errorf("unexpected extracted claims: %+v", dst)
	}
}
