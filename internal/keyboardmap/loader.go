package keyboardmap

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxExternalMapSize = 1024 * 1024

type externalFile struct {
	path string
	file *MapFile
}

func LoadForExecutable(executablePath string) LoadResult {
	result := LoadResult{Registry: BuiltInRegistry()}
	dir := filepath.Join(filepath.Dir(executablePath), "keyboard-maps")
	result.Directory = dir
	if err := prepareDirectory(dir); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map directory %q could not be prepared: %v", dir, err))
		return result
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map directory %q could not be read: %v", dir, err))
		return result
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })

	byID := map[string][]externalFile{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := readExternalMap(path)
		if readErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map %q was ignored: %v", entry.Name(), readErr))
			continue
		}
		file, parseErr := Parse(data)
		if parseErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map %q was ignored: %v", entry.Name(), parseErr))
			continue
		}
		if file.ID == "us-base" {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map %q was ignored: the protected us-base map cannot be overridden", entry.Name()))
			continue
		}
		byID[file.ID] = append(byID[file.ID], externalFile{path: path, file: file})
	}

	builtIns := cloneBuiltInFiles()
	files := maps.Clone(builtIns)
	externalIDs := map[string]bool{}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		matches := byID[id]
		if len(matches) > 1 {
			names := make([]string, 0, len(matches))
			for _, match := range matches {
				names = append(names, filepath.Base(match.path))
			}
			result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map id %q was ignored because it is defined by multiple external files: %s", id, strings.Join(names, ", ")))
			continue
		}
		files[id] = matches[0].file
		externalIDs[id] = true
	}

	for {
		registry, buildErr := buildRegistry(files)
		if buildErr == nil {
			result.Registry = registry
			return result
		}
		problemID := ""
		if typed, ok := buildErr.(registryBuildError); ok {
			problemID = typed.id
		}
		if !externalIDs[problemID] {
			result.Warnings = append(result.Warnings, fmt.Sprintf("External keyboard maps could not be activated: %v", buildErr))
			return result
		}
		result.Warnings = append(result.Warnings, fmt.Sprintf("Keyboard map %q was ignored: %v", problemID, buildErr))
		delete(externalIDs, problemID)
		if builtIn := builtIns[problemID]; builtIn != nil {
			files[problemID] = builtIn
		} else {
			delete(files, problemID)
		}
	}
}

func readExternalMap(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxExternalMapSize {
		return nil, fmt.Errorf("file exceeds the %d byte limit", maxExternalMapSize)
	}
	return os.ReadFile(path)
}

func prepareDirectory(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "_examples"), 0755); err != nil {
		return err
	}
	examples := map[string][]byte{
		"german.json":              BuiltInGermanJSON(),
		"german-map-guide.md":      GuideMarkdown(),
		"keyboard-map.schema.json": SchemaJSON(),
	}
	for name, data := range examples {
		if err := os.WriteFile(filepath.Join(dir, "_examples", name), data, 0644); err != nil {
			return fmt.Errorf("write generated example %s: %w", name, err)
		}
	}
	return nil
}
