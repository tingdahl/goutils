package auth

import (
	"encoding/json"
	"strings"
)

// OIDC claim names and issuer prefixes used across providers.
const (
	GoogleIssuer            = "https://accounts.google.com"
	GoogleIssuerLegacy      = "accounts.google.com"
	MicrosoftIssuerPrefix   = "https://login.microsoftonline.com/"
	MicrosoftIssuerPrefixV1 = "https://sts.windows.net/"

	ClaimSubject                  = "sub"
	ClaimEmail                    = "email"
	ClaimEmailVerified            = "email_verified"
	ClaimName                     = "name"
	ClaimTenantID                 = "tid"
	ClaimObjectID                 = "oid"
	ClaimEmailDomainOwnerVerified = "xms_edov"
	ClaimUPN                      = "upn"

	microsoftGuestUPNMarker = "#ext#"
)

// EmailSource identifies which claim an identity's email address was taken from.
type EmailSource string

const (
	EmailSourceNone  EmailSource = ""
	EmailSourceEmail EmailSource = ClaimEmail
	EmailSourceUPN   EmailSource = ClaimUPN
)

// IsMicrosoftIssuer reports whether the issuer is Microsoft Entra ID (v1 or v2 endpoints).
func IsMicrosoftIssuer(issuer string) bool {
	iss := strings.ToLower(strings.TrimSpace(issuer))
	return strings.HasPrefix(iss, MicrosoftIssuerPrefix) || strings.HasPrefix(iss, MicrosoftIssuerPrefixV1)
}

// ClaimIsTrue interprets boolean-like claim values (true, "true", "1", 1).
func ClaimIsTrue(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		s := strings.TrimSpace(val)
		return strings.EqualFold(s, "true") || s == "1"
	case float64:
		return val == 1
	case int:
		return val == 1
	case json.Number:
		return val.String() == "1"
	default:
		return false
	}
}

// IsEmailVerified decides whether the email claim in a verified ID token can be trusted as proof of
// ownership. The decision is based on the token issuer:
//   - If an explicit email_verified claim is present, its boolean value is honored.
//   - Microsoft Entra ID does not emit email_verified by default and its email claim is admin-controlled.
//     It is trusted when the optional xms_edov claim is true, or for personal Microsoft accounts.
//   - Google and other issuers must explicitly assert email_verified=true.
func IsEmailVerified(issuer string, claims map[string]any) bool {
	if claims == nil {
		return false
	}
	if IsMicrosoftIssuer(issuer) {
		if ClaimIsTrue(claims[ClaimEmailDomainOwnerVerified]) {
			return true
		}
		tid, _ := claims[ClaimTenantID].(string)
		if strings.EqualFold(strings.TrimSpace(tid), MicrosoftConsumerTenantID) {
			if v, exists := claims[ClaimEmailVerified]; exists && v != nil {
				return ClaimIsTrue(v)
			}
			return true
		}
		return false
	}
	return ClaimIsTrue(claims[ClaimEmailVerified])
}

// VerifiedMicrosoftUPN returns the UPN claim if it can be trusted as an email address owned by the issuing
// Entra tenant, or "" otherwise. Guest (#EXT#) UPNs and malformed values are rejected.
func VerifiedMicrosoftUPN(issuer string, claims map[string]any) string {
	if claims == nil || !IsMicrosoftIssuer(issuer) {
		return ""
	}
	upn, _ := claims[ClaimUPN].(string)
	upn = strings.TrimSpace(upn)
	if upn == "" || strings.Contains(strings.ToLower(upn), microsoftGuestUPNMarker) {
		return ""
	}
	local, domain, ok := strings.Cut(upn, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
		return ""
	}
	return upn
}

// ResolveEmailFromClaims picks the email address for a verified ID token and reports whether it is trusted:
//  1. The email claim, if the issuer vouches for it (see IsEmailVerified).
//  2. For Microsoft Entra ID only: the upn claim, whose domain Entra guarantees is verified in the tenant.
//     Used when email is missing (mail attribute unset) or set to a domain the tenant does not own.
//  3. Preferred username for Microsoft consumer personal accounts when email is omitted.
//  4. Otherwise the raw email claim (possibly empty), reported as unverified.
func ResolveEmailFromClaims(issuer string, claims map[string]any) (email string, verified bool, source EmailSource) {
	email, _ = claims[ClaimEmail].(string)
	email = strings.TrimSpace(email)
	if email == "" && IsMicrosoftIssuer(issuer) {
		tid, _ := claims[ClaimTenantID].(string)
		if strings.EqualFold(strings.TrimSpace(tid), MicrosoftConsumerTenantID) {
			if pref, ok := claims["preferred_username"].(string); ok && strings.Contains(pref, "@") {
				email = strings.TrimSpace(pref)
			}
		}
	}
	if email != "" && IsEmailVerified(issuer, claims) {
		return email, true, EmailSourceEmail
	}
	if upn := VerifiedMicrosoftUPN(issuer, claims); upn != "" {
		return upn, true, EmailSourceUPN
	}
	if email != "" {
		return email, false, EmailSourceEmail
	}
	return "", false, EmailSourceNone
}

// IdentityFromClaims builds a UserIdentity from the claims of a cryptographically verified ID token.
// The email address is chosen by ResolveEmailFromClaims.
// For Microsoft, the subject is normalized to "tid:oid" at parse time to ensure a stable, unique identity across tenants.
func IdentityFromClaims(providerID, issuer, subject string, claims map[string]any) UserIdentity {
	email, verified, _ := ResolveEmailFromClaims(issuer, claims)
	name, _ := claims[ClaimName].(string)
	tid, _ := claims[ClaimTenantID].(string)
	oid, _ := claims[ClaimObjectID].(string)

	tid = strings.TrimSpace(tid)
	oid = strings.TrimSpace(oid)

	resolvedSubject := strings.TrimSpace(subject)
	if (providerID == MicrosoftProviderID || providerID == ProviderMicrosoft || IsMicrosoftIssuer(issuer)) && tid != "" && oid != "" {
		resolvedSubject = tid + ":" + oid
	}

	if claims != nil {
		claims[ClaimSubject] = resolvedSubject
	}

	return UserIdentity{
		ProviderID:    providerID,
		Subject:       resolvedSubject,
		Email:         email,
		EmailVerified: verified,
		Name:          strings.TrimSpace(name),
		Issuer:        issuer,
		Claims:        claims,
	}
}
