package login

import "strings"

type Fields struct {
	Addr     string
	User     string
	Password string
}

// IdentityKey returns the canonical identity of a remote login without
// including its password. It is shared by credential deduplication and the
// active-session registry.
func (f Fields) IdentityKey() string {
	return canonicalIdentityPart(f.Addr) + "\x00" + canonicalIdentityPart(f.User)
}

func canonicalIdentityPart(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
