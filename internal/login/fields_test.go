package login

import "testing"

func TestFieldsIdentityKeyNormalizesAddressAndUserWithoutPassword(t *testing.T) {
	first := Fields{Addr: " ILO.Example ", User: " Administrator ", Password: "first"}
	second := Fields{Addr: "ilo.example", User: "administrator", Password: "second"}
	if first.IdentityKey() != second.IdentityKey() {
		t.Fatalf("equivalent identities differ: %q != %q", first.IdentityKey(), second.IdentityKey())
	}
	if first.IdentityKey() == (Fields{Addr: first.Addr, User: "other"}).IdentityKey() {
		t.Fatal("different users produced the same identity")
	}
}
