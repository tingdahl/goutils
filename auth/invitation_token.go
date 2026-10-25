package auth

import (
	"fmt"
	"strings"
	"time"

	"github.com/tingdahl/goutils/jwt"
)

const (
	// TokenTypInvitation is the typ claim for invitation tokens.
	TokenTypInvitation = "invitation"
)

// InvitationClaims defines claims embedded in a single-use organization/tenant invitation link.
// The token only references a pending invitation stored on the tenant/company document; it carries no personal user data.
type InvitationClaims struct {
	Typ      string `json:"typ"`                 // "invitation"
	ID       string `json:"jti"`                 // Invitation ID / UUID
	TenantID int64  `json:"tid"`                 // Target tenant or company ID
	Target   string `json:"target,omitempty"`    // Optional target entity (if string-based)
	Iat      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
}

// InvitationTokenManager handles creation and verification of signed invitation tokens.
type InvitationTokenManager struct {
	Secret string
}

// NewInvitationTokenManager initializes an InvitationTokenManager with the given secret.
func NewInvitationTokenManager(secret string) *InvitationTokenManager {
	return &InvitationTokenManager{Secret: secret}
}

// MintInvitationToken creates a signed invitation JWT for a stored invitation, expiring with it.
func (tm *InvitationTokenManager) MintInvitationToken(inviteID string, tenantID int64, expiresAt time.Time) (string, error) {
	if tm == nil || tm.Secret == "" {
		return "", jwt.ErrMissingSecret
	}
	claims := InvitationClaims{
		Typ:      TokenTypInvitation,
		ID:       strings.TrimSpace(inviteID),
		TenantID: tenantID,
		Iat:      time.Now().Unix(),
		Exp:      expiresAt.Unix(),
	}
	return jwt.Sign(claims, tm.Secret)
}

// VerifyInvitationToken validates an invitation JWT and extracts its claims.
func (tm *InvitationTokenManager) VerifyInvitationToken(tokenStr string) (*InvitationClaims, error) {
	if tm == nil || tm.Secret == "" {
		return nil, jwt.ErrMissingSecret
	}
	var claims InvitationClaims
	if err := jwt.Verify(tokenStr, tm.Secret, &claims); err != nil {
		return nil, err
	}
	if claims.Typ != TokenTypInvitation {
		return nil, fmt.Errorf("%w: unexpected token type %q", jwt.ErrInvalidToken, claims.Typ)
	}
	return &claims, nil
}
