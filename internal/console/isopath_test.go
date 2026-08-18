package console

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestISORootAllowsOnlyRegularISOFilesInsideRoot(t *testing.T) {
	rootPath := t.TempDir()
	allowedPath := filepath.Join(rootPath, "installer.ISO")
	if err := os.WriteFile(allowedPath, bytes.Repeat([]byte{0x5a}, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := NewISORoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved, name, err := root.ResolveISOPath("installer.ISO")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != allowedPath || name != "installer.ISO" {
		t.Fatalf("resolved=(%q,%q), want (%q,%q)", resolved, name, allowedPath, "installer.ISO")
	}
	if _, _, err := root.ResolveISOPath("installer.img"); err == nil {
		t.Fatal("non-ISO extension was accepted")
	}
	if _, _, err := root.ResolveISOPath("."); err == nil {
		t.Fatal("directory was accepted as ISO")
	}
}

func TestISORootRejectsTraversalAndDoesNotLeakPaths(t *testing.T) {
	rootPath := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "sensitive.iso")
	if err := os.WriteFile(outsidePath, bytes.Repeat([]byte{1}, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := NewISORoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{".." + string(filepath.Separator) + "sensitive.iso", outsidePath} {
		_, _, err := root.ResolveISOPath(input)
		if err == nil {
			t.Fatalf("path escape %q was accepted", input)
		}
		if strings.Contains(err.Error(), rootPath) || strings.Contains(err.Error(), outsidePath) {
			t.Fatalf("path error leaks filesystem data: %q", err)
		}
	}
}

func TestISORootRejectsSymlink(t *testing.T) {
	rootPath := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "target.iso")
	if err := os.WriteFile(targetPath, bytes.Repeat([]byte{2}, 2048), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(rootPath, "linked.iso")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("symlink creation is unavailable in this test environment: %v", err)
	}
	root, err := NewISORoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = root.ResolveISOPath("linked.iso")
	if err == nil {
		t.Fatal("symlink ISO was accepted")
	}
	if strings.Contains(err.Error(), targetPath) || strings.Contains(err.Error(), linkPath) {
		t.Fatalf("symlink error leaks filesystem data: %q", err)
	}
}
