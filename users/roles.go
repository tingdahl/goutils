package users

import "strings"

// HasTenantRole reports whether the user holds the specified integer role in the tenant.
func (u *UserProtoRow) HasTenantRole(tenantID int64, role int32) bool {
	if u == nil || u.TenantRoles == nil {
		return false
	}
	r, ok := u.TenantRoles[tenantID]
	return ok && r == role
}

// GetTenantRole returns the user's integer role in the tenant, if any.
func (u *UserProtoRow) GetTenantRole(tenantID int64) (int32, bool) {
	if u == nil || u.TenantRoles == nil {
		return 0, false
	}
	r, ok := u.TenantRoles[tenantID]
	return r, ok
}

// HasLinkedIdentity reports whether the user has at least one non-empty OIDC subject linked.
func (u *UserProtoRow) HasLinkedIdentity() bool {
	if u == nil {
		return false
	}
	for _, sub := range u.Identities {
		if strings.TrimSpace(sub) != "" {
			return true
		}
	}
	return false
}
