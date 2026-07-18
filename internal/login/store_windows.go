package login

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const credentialDirName = "ACP-ILO-KVM"

var (
	crypt32           = syscall.NewLazyDLL("crypt32.dll")
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procProtectData   = crypt32.NewProc("CryptProtectData")
	procUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree     = kernel32.NewProc("LocalFree")

	credentialPathFunc = defaultCredentialPath
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

type credentialStore struct {
	Entries []credentialEntry `json:"entries"`
}

type credentialEntry struct {
	Addr          string `json:"addr"`
	User          string `json:"user"`
	PasswordDPAPI string `json:"password_dpapi,omitempty"`
}

func loadCredentialStore() credentialStore {
	path, err := credentialPath()
	if err != nil {
		return credentialStore{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return loadLegacyCredentialStore(path)
	}
	var s credentialStore
	if err := json.Unmarshal(b, &s); err != nil {
		return credentialStore{}
	}
	return s
}

func (s credentialStore) save() error {
	path, err := credentialPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}

func credentialPath() (string, error) {
	return credentialPathFunc()
}

func defaultCredentialPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, credentialDirName, "cred.json"), nil
}

func legacyCredentialPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), "cred.json"), nil
}

func loadLegacyCredentialStore(newPath string) credentialStore {
	legacyPath, err := legacyCredentialPath()
	if err != nil || filepath.Clean(legacyPath) == filepath.Clean(newPath) {
		return credentialStore{}
	}
	b, err := os.ReadFile(legacyPath)
	if err != nil {
		return credentialStore{}
	}
	var s credentialStore
	if err := json.Unmarshal(b, &s); err != nil {
		return credentialStore{}
	}
	if len(s.Entries) > 0 {
		_ = s.save()
	}
	return s
}

func (s credentialStore) find(addr string) (credentialEntry, bool) {
	addr = strings.TrimSpace(addr)
	for _, e := range s.Entries {
		if strings.EqualFold(strings.TrimSpace(e.Addr), addr) {
			return e, true
		}
	}
	return credentialEntry{}, false
}

func (s *credentialStore) upsert(fields Fields, savePassword bool) error {
	fields.Addr = strings.TrimSpace(fields.Addr)
	fields.User = strings.TrimSpace(fields.User)
	entry := credentialEntry{Addr: fields.Addr, User: fields.User}
	if savePassword {
		secret, err := protectString(fields.Password)
		if err != nil {
			return err
		}
		entry.PasswordDPAPI = secret
	} else {
		for _, e := range s.Entries {
			if sameCredential(e, fields.Addr, fields.User) {
				entry.PasswordDPAPI = e.PasswordDPAPI
				break
			}
		}
	}
	filtered := []credentialEntry{entry}
	for _, e := range s.Entries {
		if sameCredential(e, fields.Addr, fields.User) {
			continue
		}
		filtered = append(filtered, e)
	}
	s.Entries = filtered
	return s.save()
}

func (s *credentialStore) delete(addr, user string) error {
	var filtered []credentialEntry
	for _, e := range s.Entries {
		if sameCredential(e, addr, user) {
			continue
		}
		filtered = append(filtered, e)
	}
	s.Entries = filtered
	return s.save()
}

func sameCredential(e credentialEntry, addr, user string) bool {
	return (Fields{Addr: e.Addr, User: e.User}).IdentityKey() == (Fields{Addr: addr, User: user}).IdentityKey()
}

func (e credentialEntry) password() (string, bool) {
	if e.PasswordDPAPI == "" {
		return "", false
	}
	p, err := unprotectString(e.PasswordDPAPI)
	if err != nil {
		return "", false
	}
	return p, true
}

func protectString(s string) (string, error) {
	out, err := cryptProtect([]byte(s))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

func unprotectString(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	out, err := cryptUnprotect(b)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func cryptProtect(in []byte) ([]byte, error) {
	if len(in) == 0 {
		in = []byte{0}
	}
	src := dataBlob{cbData: uint32(len(in)), pbData: &in[0]}
	var dst dataBlob
	r, _, err := procProtectData.Call(uintptr(unsafe.Pointer(&src)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&dst)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(dst.pbData)))
	return blobBytes(dst), nil
}

func cryptUnprotect(in []byte) ([]byte, error) {
	if len(in) == 0 {
		return nil, errors.New("empty DPAPI payload")
	}
	src := dataBlob{cbData: uint32(len(in)), pbData: &in[0]}
	var dst dataBlob
	r, _, err := procUnprotectData.Call(uintptr(unsafe.Pointer(&src)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&dst)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(dst.pbData)))
	out := blobBytes(dst)
	if len(out) == 1 && out[0] == 0 {
		return nil, nil
	}
	return out, nil
}

func blobBytes(b dataBlob) []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	src := unsafe.Slice(b.pbData, b.cbData)
	out := make([]byte, len(src))
	copy(out, src)
	return out
}
