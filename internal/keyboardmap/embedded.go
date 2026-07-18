package keyboardmap

import (
	_ "embed"
	"fmt"
	"sync"
)

var (
	//go:embed maps/us-base.json
	builtInUS []byte
	//go:embed maps/german.json
	builtInGerman []byte
	//go:embed assets/german-map-guide.md
	builtInGuide []byte
	//go:embed assets/keyboard-map.schema.json
	builtInSchema []byte
)

var (
	builtInOnce     sync.Once
	builtInRegistry *Registry
	builtInFiles    map[string]*MapFile
	builtInErr      error
)

func BuiltInRegistry() *Registry {
	loadBuiltIns()
	if builtInErr != nil {
		panic(builtInErr)
	}
	return builtInRegistry
}

func BuiltInGermanJSON() []byte { return append([]byte(nil), builtInGerman...) }
func GuideMarkdown() []byte     { return append([]byte(nil), builtInGuide...) }
func SchemaJSON() []byte        { return append([]byte(nil), builtInSchema...) }

func loadBuiltIns() {
	builtInOnce.Do(func() {
		builtInFiles = map[string]*MapFile{}
		for name, data := range map[string][]byte{"us-base.json": builtInUS, "german.json": builtInGerman} {
			file, err := Parse(data)
			if err != nil {
				builtInErr = fmt.Errorf("invalid embedded keyboard map %s: %w", name, err)
				return
			}
			builtInFiles[file.ID] = file
		}
		builtInRegistry, builtInErr = buildRegistry(builtInFiles)
		if builtInErr != nil {
			builtInErr = fmt.Errorf("build embedded keyboard maps: %w", builtInErr)
		}
	})
}

func cloneBuiltInFiles() map[string]*MapFile {
	loadBuiltIns()
	if builtInErr != nil {
		panic(builtInErr)
	}
	out := make(map[string]*MapFile, len(builtInFiles))
	for id, file := range builtInFiles {
		copy := *file
		out[id] = &copy
	}
	return out
}
