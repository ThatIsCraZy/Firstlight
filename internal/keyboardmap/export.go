package keyboardmap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ExportPaths(jsonPath string) (string, string) {
	if !strings.EqualFold(filepath.Ext(jsonPath), ".json") {
		jsonPath += ".json"
	}
	return jsonPath, strings.TrimSuffix(jsonPath, filepath.Ext(jsonPath)) + ".md"
}

// ExportBuiltInGerman writes the built-in German reference and its LLM guide as one recoverable pair.
func ExportBuiltInGerman(jsonPath string) (string, string, error) {
	jsonPath, markdownPath := ExportPaths(jsonPath)
	for _, target := range []string{jsonPath, markdownPath} {
		if info, err := os.Stat(target); err == nil && !info.Mode().IsRegular() {
			return jsonPath, markdownPath, fmt.Errorf("export target is not a regular file: %s", target)
		} else if err != nil && !os.IsNotExist(err) {
			return jsonPath, markdownPath, err
		}
	}
	data := BuiltInGermanJSON()
	if _, err := Parse(data); err != nil {
		return jsonPath, markdownPath, fmt.Errorf("validate built-in German map: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0755); err != nil {
		return jsonPath, markdownPath, err
	}
	jsonStage, err := writeStage(jsonPath, data)
	if err != nil {
		return jsonPath, markdownPath, err
	}
	markdownStage, err := writeStage(markdownPath, GuideMarkdown())
	if err != nil {
		_ = os.Remove(jsonStage)
		return jsonPath, markdownPath, err
	}
	if err := commitPair(jsonStage, jsonPath, markdownStage, markdownPath); err != nil {
		_ = os.Remove(jsonStage)
		_ = os.Remove(markdownStage)
		return jsonPath, markdownPath, err
	}
	return jsonPath, markdownPath, nil
}

func writeStage(target string, data []byte) (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		path := fmt.Sprintf("%s.hpeirc-new-%d-%d", target, os.Getpid(), attempt)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			_ = os.Remove(path)
			return "", writeErr
		}
		if closeErr != nil {
			_ = os.Remove(path)
			return "", closeErr
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate staging file next to %q", target)
}

type pairItem struct {
	stage, target, backup string
	committed             bool
}

func commitPair(jsonStage, jsonTarget, markdownStage, markdownTarget string) error {
	items := []*pairItem{{stage: jsonStage, target: jsonTarget}, {stage: markdownStage, target: markdownTarget}}
	for index, current := range items {
		if _, err := os.Stat(current.target); err == nil {
			current.backup = fmt.Sprintf("%s.hpeirc-backup-%d-%d", current.target, os.Getpid(), index)
			if _, backupErr := os.Stat(current.backup); backupErr == nil {
				restoreBackups(items)
				return fmt.Errorf("backup path already exists: %s", current.backup)
			} else if !os.IsNotExist(backupErr) {
				restoreBackups(items)
				return backupErr
			}
			if err := os.Rename(current.target, current.backup); err != nil {
				restoreBackups(items)
				return err
			}
		} else if !os.IsNotExist(err) {
			restoreBackups(items)
			return err
		}
	}
	for _, current := range items {
		if err := os.Rename(current.stage, current.target); err != nil {
			rollbackCommit(items)
			return err
		}
		current.committed = true
	}
	for _, current := range items {
		if current.backup != "" {
			_ = os.Remove(current.backup)
		}
	}
	return nil
}

func restoreBackups(items []*pairItem) {
	for index := len(items) - 1; index >= 0; index-- {
		current := items[index]
		if current.backup != "" {
			_ = os.Rename(current.backup, current.target)
			current.backup = ""
		}
	}
}

func rollbackCommit(items []*pairItem) {
	for index := len(items) - 1; index >= 0; index-- {
		current := items[index]
		if current.committed {
			_ = os.Remove(current.target)
			current.committed = false
		}
	}
	restoreBackups(items)
}
