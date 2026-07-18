package keyboardmap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInGermanPreservesPhysicalAndTextMappings(t *testing.T) {
	registry := BuiltInRegistry()
	tests := []struct {
		input string
		state State
		want  Stroke
	}{
		{input: "Q", state: StatePlain, want: Stroke{Key: 20}},
		{input: "Q", state: StateAltGr, want: Stroke{Key: 31, Modifiers: 2}},
		{input: "OEM_2", state: StatePlain, want: Stroke{Key: 32, Modifiers: 2}},
		{input: "OEM_2", state: StateShift, want: Stroke{Key: 52}},
		{input: "OEM_4", state: StatePlain, want: Stroke{Suppress: true}},
	}
	for _, test := range tests {
		got, ok := registry.ResolvePhysical("german", test.input, test.state)
		if !ok {
			t.Fatalf("ResolvePhysical(german, %s, %s) was not found", test.input, test.state)
		}
		if got != test.want {
			t.Fatalf("ResolvePhysical(german, %s, %s)=%+v want %+v", test.input, test.state, got, test.want)
		}
	}
	strokes, ok := registry.ResolveText("german", '@')
	if !ok || len(strokes) != 1 || strokes[0] != (Stroke{Key: 31, Modifiers: 2}) {
		t.Fatalf("German inherited text @=%+v found=%v", strokes, ok)
	}
	if _, ok := registry.ResolveText("german", 'ä'); ok {
		t.Fatal("German text unexpectedly supports ä")
	}
}

func TestParseRejectsInvalidCatalogAndDuplicateRules(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{
			name: "unknown HID",
			json: `{"schemaVersion":1,"id":"test","displayName":"Test","sourceLocale":"fr-FR","targetLocale":"en-US","extends":"us-base","physical":[{"input":"A","plain":{"key":"999"}}],"text":[]}`,
			want: "unknown HID key",
		},
		{
			name: "duplicate input",
			json: `{"schemaVersion":1,"id":"test","displayName":"Test","sourceLocale":"fr-FR","targetLocale":"en-US","extends":"us-base","physical":[{"input":"A","plain":{"key":"A"}},{"input":"A","plain":{"key":"B"}}],"text":[]}`,
			want: "duplicate input key",
		},
		{
			name: "wrong target",
			json: `{"schemaVersion":1,"id":"test","displayName":"Test","sourceLocale":"fr-FR","targetLocale":"fr-FR","extends":"us-base","physical":[],"text":[]}`,
			want: "targetLocale must be en-US",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.json))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestGenericVKAndHIDTokensNeedNoCatalogEntry(t *testing.T) {
	file, err := Parse([]byte(`{
  "schemaVersion":1,"id":"extended","displayName":"Extended",
  "sourceLocale":"fr-FR","targetLocale":"en-US","extends":"us-base",
  "physical":[{"input":"VK_0x7C","plain":{"key":"HID_0x68"}}],"text":[]
}`))
	if err != nil {
		t.Fatal(err)
	}
	files := cloneBuiltInFiles()
	files[file.ID] = file
	registry, err := buildRegistry(files)
	if err != nil {
		t.Fatal(err)
	}
	stroke, ok := registry.ResolvePhysical("extended", "VK_0x7C", StatePlain)
	if !ok || stroke != (Stroke{Key: 0x68}) {
		t.Fatalf("generic mapping=%+v found=%v", stroke, ok)
	}
	for _, invalid := range []string{
		`{"schemaVersion":1,"id":"bad","displayName":"Bad","sourceLocale":"x","targetLocale":"en-US","extends":"us-base","physical":[{"input":"VK_0xGG","plain":{"key":"A"}}],"text":[]}`,
		`{"schemaVersion":1,"id":"bad","displayName":"Bad","sourceLocale":"x","targetLocale":"en-US","extends":"us-base","physical":[{"input":"A","plain":{"key":"HID_0xE1"}}],"text":[]}`,
	} {
		if _, err := Parse([]byte(invalid)); err == nil {
			t.Fatalf("invalid generic token was accepted: %s", invalid)
		}
	}
}

func TestLoadAddsExternalMapAndCreatesExamples(t *testing.T) {
	dir := workspaceTestDir(t)
	exe := filepath.Join(dir, "hpeirc.exe")
	mapDir := filepath.Join(dir, "keyboard-maps")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(mapDir, "french.json"), `{
  "schemaVersion": 1,
  "id": "french",
  "displayName": "French",
  "sourceLocale": "fr-FR",
  "targetLocale": "en-US",
  "extends": "us-base",
  "physical": [{"input":"A","plain":{"key":"Q"}}],
  "text": []
}`)
	result := LoadForExecutable(exe)
	if len(result.Warnings) != 0 {
		t.Fatalf("Load warnings=%v", result.Warnings)
	}
	info, ok := result.Registry.Info("french")
	if !ok || info.DisplayName != "French" {
		t.Fatalf("French info=%+v found=%v", info, ok)
	}
	stroke, ok := result.Registry.ResolvePhysical("french", "A", StatePlain)
	if !ok || stroke.Key != 20 {
		t.Fatalf("French A=%+v found=%v", stroke, ok)
	}
	for _, name := range []string{"german.json", "german-map-guide.md", "keyboard-map.schema.json"} {
		if _, err := os.Stat(filepath.Join(mapDir, "_examples", name)); err != nil {
			t.Fatalf("generated example %s: %v", name, err)
		}
	}
}

func TestLoadExternalGermanReplacementAndInvalidFallback(t *testing.T) {
	dir := workspaceTestDir(t)
	exe := filepath.Join(dir, "hpeirc.exe")
	mapDir := filepath.Join(dir, "keyboard-maps")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		t.Fatal(err)
	}
	override := `{
  "schemaVersion":1,"id":"german","displayName":"German override",
  "sourceLocale":"de-DE","targetLocale":"en-US","extends":"us-base",
  "physical":[{"input":"Q","plain":{"key":"Z"}}],"text":[]
}`
	path := filepath.Join(mapDir, "german.json")
	writeTestFile(t, path, override)
	result := LoadForExecutable(exe)
	stroke, ok := result.Registry.ResolvePhysical("german", "Q", StatePlain)
	if !ok || stroke.Key != 29 {
		t.Fatalf("German override Q=%+v found=%v warnings=%v", stroke, ok, result.Warnings)
	}
	writeTestFile(t, path, `{"schemaVersion":1,"id":"german"}`)
	result = LoadForExecutable(exe)
	if len(result.Warnings) == 0 {
		t.Fatal("invalid German override did not produce a warning")
	}
	stroke, ok = result.Registry.ResolvePhysical("german", "Q", StatePlain)
	if !ok || stroke.Key != 20 {
		t.Fatalf("German fallback Q=%+v found=%v warnings=%v", stroke, ok, result.Warnings)
	}
}

func TestLoadRejectsProtectedDuplicateAndCyclicMaps(t *testing.T) {
	dir := workspaceTestDir(t)
	exe := filepath.Join(dir, "hpeirc.exe")
	mapDir := filepath.Join(dir, "keyboard-maps")
	if err := os.MkdirAll(mapDir, 0755); err != nil {
		t.Fatal(err)
	}
	valid := func(id, extends string) string {
		return `{"schemaVersion":1,"id":"` + id + `","displayName":"` + id + `","sourceLocale":"fr-FR","targetLocale":"en-US","extends":"` + extends + `","physical":[],"text":[]}`
	}
	writeTestFile(t, filepath.Join(mapDir, "duplicate-a.json"), valid("duplicate", "us-base"))
	writeTestFile(t, filepath.Join(mapDir, "duplicate-b.json"), valid("duplicate", "us-base"))
	writeTestFile(t, filepath.Join(mapDir, "base.json"), `{"schemaVersion":1,"id":"us-base","displayName":"Bad base","sourceLocale":"en-US","targetLocale":"en-US","physical":[],"text":[]}`)
	writeTestFile(t, filepath.Join(mapDir, "cycle-a.json"), valid("cycle-a", "cycle-b"))
	writeTestFile(t, filepath.Join(mapDir, "cycle-b.json"), valid("cycle-b", "cycle-a"))
	result := LoadForExecutable(exe)
	if len(result.Warnings) < 3 {
		t.Fatalf("warnings=%v", result.Warnings)
	}
	for _, id := range []string{"duplicate", "cycle-a", "cycle-b"} {
		if _, ok := result.Registry.Info(id); ok {
			t.Fatalf("invalid map %q was loaded", id)
		}
	}
	if _, ok := result.Registry.Info("us-base"); !ok {
		t.Fatal("built-in us-base was lost")
	}
}

func TestExportBuiltInGermanPair(t *testing.T) {
	dir := workspaceTestDir(t)
	jsonPath := filepath.Join(dir, "custom-name")
	wantJSON, wantMarkdown := ExportPaths(jsonPath)
	gotJSON, gotMarkdown, err := ExportBuiltInGerman(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if gotJSON != wantJSON || gotMarkdown != wantMarkdown {
		t.Fatalf("paths=(%q,%q) want (%q,%q)", gotJSON, gotMarkdown, wantJSON, wantMarkdown)
	}
	jsonData, err := os.ReadFile(gotJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(jsonData, BuiltInGermanJSON()) {
		t.Fatal("exported JSON differs from embedded German reference")
	}
	if _, err := Parse(jsonData); err != nil {
		t.Fatalf("exported JSON is invalid: %v", err)
	}
	guide, err := os.ReadFile(gotMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"source language keyboard -> en-US remote keyboard", "Allowed physical input names", "French map skeleton", "Copy-and-paste LLM prompt", "VK_0xNN", "HID_0xNN"} {
		if !bytes.Contains(guide, []byte(required)) {
			t.Errorf("guide does not contain %q", required)
		}
	}
	writeTestFile(t, gotJSON, "old json")
	writeTestFile(t, gotMarkdown, "old markdown")
	if _, _, err := ExportBuiltInGerman(gotJSON); err != nil {
		t.Fatalf("overwrite export: %v", err)
	}
	jsonData, _ = os.ReadFile(gotJSON)
	if !bytes.Equal(jsonData, BuiltInGermanJSON()) {
		t.Fatal("overwrite did not replace JSON")
	}
}

func TestExportFailureLeavesExistingPairUnchanged(t *testing.T) {
	dir := workspaceTestDir(t)
	jsonPath := filepath.Join(dir, "german.json")
	markdownPath := filepath.Join(dir, "german.md")
	writeTestFile(t, jsonPath, "old json")
	if err := os.Mkdir(markdownPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExportBuiltInGerman(jsonPath); err == nil {
		t.Fatal("export unexpectedly replaced a directory target")
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old json" {
		t.Fatalf("existing JSON changed to %q", data)
	}
	if info, err := os.Stat(markdownPath); err != nil || !info.IsDir() {
		t.Fatalf("existing markdown directory was changed: info=%v err=%v", info, err)
	}
}

func workspaceTestDir(t *testing.T) string {
	t.Helper()
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
	base := filepath.Join(root, ".gotmp", "keyboardmap-tests")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
