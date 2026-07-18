package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withCredentialPath(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("\\", "_", "/", "_", ":", "_", " ", "_").Replace(t.Name())
	path := filepath.Join("..", "..", ".gotmp", "login-tests", name, "cred.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	old := credentialPathFunc
	credentialPathFunc = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialPathFunc = old })
	return path
}

func TestCredentialStoreMissingAndCorrupt(t *testing.T) {
	path := withCredentialPath(t)
	if got := loadCredentialStore(); len(got.Entries) != 0 {
		t.Fatalf("missing store entries=%#v", got.Entries)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadCredentialStore(); len(got.Entries) != 0 {
		t.Fatalf("corrupt store entries=%#v", got.Entries)
	}
}

func TestDefaultCredentialPathUsesLocalAppData(t *testing.T) {
	root := filepath.Join("..", "..", ".gotmp", "localappdata")
	t.Setenv("LOCALAPPDATA", root)
	got, err := defaultCredentialPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, credentialDirName, "cred.json")
	if got != want {
		t.Fatalf("path=%q want=%q", got, want)
	}
}

func TestCredentialStoreUpsertOrderDedupeUnlimited(t *testing.T) {
	withCredentialPath(t)
	var s credentialStore
	for i := 0; i < 12; i++ {
		err := s.upsert(Fields{Addr: string(rune('a' + i)), User: "u", Password: "p"}, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.Entries) != 12 {
		t.Fatalf("entries=%d", len(s.Entries))
	}
	if s.Entries[0].Addr != "l" || s.Entries[len(s.Entries)-1].Addr != "a" {
		t.Fatalf("bad order: %#v", s.Entries)
	}
	if err := s.upsert(Fields{Addr: "f", User: "u", Password: "new"}, false); err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 12 || s.Entries[0].Addr != "f" || s.Entries[0].PasswordDPAPI != "" {
		t.Fatalf("bad dedupe/update: %#v", s.Entries[0])
	}
}

func TestCredentialStoreDeleteExactEntry(t *testing.T) {
	withCredentialPath(t)
	s := credentialStore{Entries: []credentialEntry{
		{Addr: "ilo", User: "Administrator"},
		{Addr: "ilo", User: "Other"},
		{Addr: "ilo2", User: "Administrator"},
	}}
	if err := s.delete("ilo", "Administrator"); err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 2 {
		t.Fatalf("entries=%#v", s.Entries)
	}
	if hasCredential(s, "ilo", "Administrator") {
		t.Fatalf("deleted entry still present: %#v", s.Entries)
	}
	if !hasCredential(s, "ilo", "Other") {
		t.Fatalf("other user was removed: %#v", s.Entries)
	}
	if !hasCredential(s, "ilo2", "Administrator") {
		t.Fatalf("other host was removed: %#v", s.Entries)
	}
}

func hasCredential(store credentialStore, addr, user string) bool {
	for _, entry := range store.Entries {
		if sameCredential(entry, addr, user) {
			return true
		}
	}
	return false
}

func TestConnectionListModelKeepsStoreOrderAndValues(t *testing.T) {
	m := newConnectionListModel([]credentialEntry{
		{Addr: "new", User: "admin"},
		{Addr: "old", User: "root"},
	})
	if m.RowCount() != 2 {
		t.Fatalf("rows=%d", m.RowCount())
	}
	if got := m.Value(0, 1); got != "new" {
		t.Fatalf("addr=%v", got)
	}
	if got := m.Value(0, 2); got != "admin" {
		t.Fatalf("user=%v", got)
	}
	entry, ok := m.entry(1)
	if !ok || entry.Addr != "old" || entry.User != "root" {
		t.Fatalf("entry=%#v ok=%v", entry, ok)
	}
}

func TestCredentialStoreKeepsExistingPasswordWhenSaveUnchecked(t *testing.T) {
	withCredentialPath(t)
	var s credentialStore
	if err := s.upsert(Fields{Addr: "ilo", User: "Administrator", Password: "old"}, true); err != nil {
		t.Fatal(err)
	}
	secret := s.Entries[0].PasswordDPAPI
	if secret == "" {
		t.Fatal("expected protected password")
	}
	if err := s.upsert(Fields{Addr: "ilo", User: "Administrator", Password: "new"}, false); err != nil {
		t.Fatal(err)
	}
	if s.Entries[0].PasswordDPAPI != secret {
		t.Fatalf("password was not preserved")
	}
	got, ok := s.Entries[0].password()
	if !ok || got != "old" {
		t.Fatalf("preserved password=%q ok=%v", got, ok)
	}
}

func TestDPAPIRoundTrip(t *testing.T) {
	secret, err := protectString("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || secret == "test-password" {
		t.Fatalf("bad protected value: %q", secret)
	}
	got, err := unprotectString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if got != "test-password" {
		t.Fatalf("got %q", got)
	}
}
