package console

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ISORoot is an immutable, canonical directory from which the MCP bridge may
// serve ISO files. It intentionally keeps its filesystem path private.
type ISORoot struct {
	path string
}

func NewISORoot(path string) (*ISORoot, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("ISO root is invalid")
	}
	absolute = filepath.Clean(absolute)
	if err := rejectReparsePoints(absolute); err != nil {
		return nil, errors.New("ISO root must be an existing directory without reparse points")
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return nil, errors.New("ISO root must be an existing directory")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, errors.New("ISO root cannot be resolved safely")
	}
	return &ISORoot{path: filepath.Clean(canonical)}, nil
}

func (r *ISORoot) ResolveISOPath(path string) (string, string, error) {
	if r == nil {
		return "", "", errors.New("ISO mounting is disabled; configure -iso-root")
	}
	path = strings.TrimSpace(path)
	if path == "" || !strings.EqualFold(filepath.Ext(path), ".iso") {
		return "", "", errors.New("only regular .iso files under the configured ISO root are allowed")
	}
	if !filepath.IsAbs(path) && filepath.VolumeName(path) != "" {
		return "", "", errors.New("only regular .iso files under the configured ISO root are allowed")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(r.path, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil || !pathWithin(r.path, abs) {
		return "", "", errors.New("only regular .iso files under the configured ISO root are allowed")
	}
	if err := rejectReparsePoints(abs); err != nil {
		return "", "", errors.New("ISO path is unavailable or contains a reparse point")
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil || !pathWithin(r.path, canonical) {
		return "", "", errors.New("ISO path cannot be resolved safely")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return "", "", errors.New("ISO path must be a non-empty regular file")
	}
	return canonical, filepath.Base(canonical), nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func rejectReparsePoints(path string) error {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimLeft(rest, string(filepath.Separator))
	current := volume
	if filepath.IsAbs(path) {
		current += string(filepath.Separator)
	}
	components := strings.FieldsFunc(rest, func(r rune) bool { return r == rune(filepath.Separator) })
	if len(components) == 0 {
		components = []string{"."}
	}
	for _, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if isReparsePoint(info) {
			return errors.New("reparse point")
		}
	}
	return nil
}
