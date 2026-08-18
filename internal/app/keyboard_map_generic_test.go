//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lxn/walk"

	"firstlight/internal/keyboardmap"
	"firstlight/internal/kvm"
)

func TestGenericVirtualKeyMappingFromJSON(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("go.mod not found")
		}
		root = parent
	}
	base := filepath.Join(root, ".gotmp", "app-keyboardmap-tests")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	mapDir := filepath.Join(dir, "keyboard-maps")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "schemaVersion":1,"id":"extended","displayName":"Extended",
  "sourceLocale":"fr-FR","targetLocale":"en-US","extends":"us-base",
  "physical":[{"input":"VK_0x7C","plain":{"key":"HID_0x68"}}],"text":[]
}`)
	if err := os.WriteFile(filepath.Join(mapDir, "extended.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	loaded := keyboardmap.LoadForExecutable(filepath.Join(dir, "Firstlight.exe"))
	if len(loaded.Warnings) != 0 {
		t.Fatalf("load warnings=%v", loaded.Warnings)
	}
	report := keyboardReportForRegistry(loaded.Registry, keyboardLayout("extended"), keys(walk.Key(0x7c)))
	if want := kvm.KeyboardReport(0, 0x68); report != want {
		t.Fatalf("generic F13 report=%x want=%x", report, want)
	}
}
