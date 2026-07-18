package keyboardmap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

type resolvedRule struct {
	plain *Stroke
	shift *Stroke
	altGr *Stroke
}

func (r resolvedRule) stroke(state State) *Stroke {
	switch state {
	case StatePlain:
		return r.plain
	case StateShift:
		return r.shift
	case StateAltGr:
		return r.altGr
	default:
		return nil
	}
}

type resolvedMap struct {
	info       MapInfo
	selectable bool
	physical   map[string]resolvedRule
	text       map[rune][]Stroke
}

type Registry struct {
	maps map[string]*resolvedMap
}

func Parse(data []byte) (*MapFile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file MapFile
	if err := dec.Decode(&file); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, err
	}
	if err := validateFile(&file); err != nil {
		return nil, err
	}
	return &file, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected data after JSON document")
		}
		return err
	}
	return nil
}

func validateFile(file *MapFile) error {
	if file.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion must be 1")
	}
	if !idPattern.MatchString(file.ID) {
		return fmt.Errorf("id %q must be lowercase and contain only letters, digits, '.', '_' or '-'", file.ID)
	}
	if strings.TrimSpace(file.DisplayName) == "" {
		return fmt.Errorf("displayName is required")
	}
	if strings.TrimSpace(file.SourceLocale) == "" {
		return fmt.Errorf("sourceLocale is required")
	}
	if file.TargetLocale != "en-US" {
		return fmt.Errorf("targetLocale must be en-US")
	}
	seenPhysical := map[string]bool{}
	for i := range file.Physical {
		rule := &file.Physical[i]
		if !validInputKey(rule.Input) {
			return fmt.Errorf("physical[%d]: unknown input key %q", i, rule.Input)
		}
		if seenPhysical[rule.Input] {
			return fmt.Errorf("physical[%d]: duplicate input key %q", i, rule.Input)
		}
		seenPhysical[rule.Input] = true
		if rule.Plain == nil && rule.Shift == nil && rule.AltGr == nil {
			return fmt.Errorf("physical[%d]: at least one of plain, shift or altGr is required", i)
		}
		for name, stroke := range map[string]*StrokeSpec{"plain": rule.Plain, "shift": rule.Shift, "altGr": rule.AltGr} {
			if stroke != nil {
				if _, err := compileStroke(*stroke, true); err != nil {
					return fmt.Errorf("physical[%d].%s: %w", i, name, err)
				}
			}
		}
	}
	seenText := map[rune]bool{}
	for i, rule := range file.Text {
		if utf8.RuneCountInString(rule.Char) != 1 {
			return fmt.Errorf("text[%d].char must contain exactly one Unicode character", i)
		}
		r, _ := utf8.DecodeRuneInString(rule.Char)
		if seenText[r] {
			return fmt.Errorf("text[%d]: duplicate character %q", i, rule.Char)
		}
		seenText[r] = true
		if len(rule.Strokes) == 0 {
			return fmt.Errorf("text[%d].strokes must not be empty", i)
		}
		for j, stroke := range rule.Strokes {
			compiled, err := compileStroke(stroke, false)
			if err != nil {
				return fmt.Errorf("text[%d].strokes[%d]: %w", i, j, err)
			}
			if compiled.Suppress {
				return fmt.Errorf("text[%d].strokes[%d]: suppress is not valid for text", i, j)
			}
		}
	}
	return nil
}

func compileStroke(spec StrokeSpec, allowSuppress bool) (Stroke, error) {
	if spec.Suppress {
		if !allowSuppress {
			return Stroke{}, fmt.Errorf("suppress is not allowed here")
		}
		if spec.Key != "" || len(spec.Modifiers) != 0 {
			return Stroke{}, fmt.Errorf("suppress cannot be combined with key or modifiers")
		}
		return Stroke{Suppress: true}, nil
	}
	hid, ok := resolveHIDKey(spec.Key)
	if !ok {
		return Stroke{}, fmt.Errorf("unknown HID key %q", spec.Key)
	}
	stroke := Stroke{Key: hid}
	seen := map[string]bool{}
	for _, name := range spec.Modifiers {
		value, ok := modifiers[name]
		if !ok {
			return Stroke{}, fmt.Errorf("unknown modifier %q", name)
		}
		if seen[name] {
			return Stroke{}, fmt.Errorf("duplicate modifier %q", name)
		}
		seen[name] = true
		stroke.Modifiers |= value
	}
	return stroke, nil
}

type registryBuildError struct {
	id  string
	err error
}

func (e registryBuildError) Error() string { return e.err.Error() }

func buildRegistry(files map[string]*MapFile) (*Registry, error) {
	resolved := map[string]*resolvedMap{}
	visiting := map[string]bool{}
	var resolve func(string) (*resolvedMap, error)
	resolve = func(id string) (*resolvedMap, error) {
		if found := resolved[id]; found != nil {
			return found, nil
		}
		file := files[id]
		if file == nil {
			return nil, registryBuildError{id: id, err: fmt.Errorf("map %q does not exist", id)}
		}
		if visiting[id] {
			return nil, registryBuildError{id: id, err: fmt.Errorf("map %q has an inheritance cycle", id)}
		}
		visiting[id] = true
		current := &resolvedMap{
			info:       MapInfo{ID: file.ID, DisplayName: file.DisplayName, SourceLocale: file.SourceLocale, TargetLocale: file.TargetLocale},
			selectable: file.Selectable == nil || *file.Selectable,
			physical:   map[string]resolvedRule{},
			text:       map[rune][]Stroke{},
		}
		if file.Extends != "" {
			parent, err := resolve(file.Extends)
			if err != nil {
				delete(visiting, id)
				if buildErr, ok := err.(registryBuildError); ok && buildErr.id == file.Extends {
					return nil, registryBuildError{id: id, err: fmt.Errorf("map %q extends invalid map %q: %w", id, file.Extends, buildErr.err)}
				}
				return nil, err
			}
			for key, rule := range parent.physical {
				current.physical[key] = rule
			}
			for char, strokes := range parent.text {
				current.text[char] = append([]Stroke(nil), strokes...)
			}
		}
		for _, rule := range file.Physical {
			compiled := current.physical[rule.Input]
			if rule.Plain != nil {
				value, _ := compileStroke(*rule.Plain, true)
				compiled.plain = &value
			}
			if rule.Shift != nil {
				value, _ := compileStroke(*rule.Shift, true)
				compiled.shift = &value
			}
			if rule.AltGr != nil {
				value, _ := compileStroke(*rule.AltGr, true)
				compiled.altGr = &value
			}
			current.physical[rule.Input] = compiled
		}
		for _, rule := range file.Text {
			r, _ := utf8.DecodeRuneInString(rule.Char)
			strokes := make([]Stroke, 0, len(rule.Strokes))
			for _, spec := range rule.Strokes {
				stroke, _ := compileStroke(spec, false)
				strokes = append(strokes, stroke)
			}
			current.text[r] = strokes
		}
		delete(visiting, id)
		resolved[id] = current
		return current, nil
	}
	for id := range files {
		if _, err := resolve(id); err != nil {
			if buildErr, ok := err.(registryBuildError); ok {
				if _, externalExists := files[buildErr.id]; !externalExists {
					buildErr.id = id
				}
				return nil, buildErr
			}
			return nil, registryBuildError{id: id, err: err}
		}
	}
	return &Registry{maps: resolved}, nil
}

func (r *Registry) Selectable() []MapInfo {
	if r == nil {
		return nil
	}
	out := []MapInfo{}
	for _, entry := range r.maps {
		if entry.selectable {
			out = append(out, entry.info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].DisplayName) < strings.ToLower(out[j].DisplayName)
	})
	return out
}

func (r *Registry) Info(id string) (MapInfo, bool) {
	if r == nil || r.maps[id] == nil {
		return MapInfo{}, false
	}
	return r.maps[id].info, true
}

func (r *Registry) HasPhysical(id, input string, state State) bool {
	entry := r.mapForID(id)
	if entry == nil {
		return false
	}
	rule, ok := entry.physical[input]
	return ok && rule.stroke(state) != nil
}

func (r *Registry) ResolvePhysical(id, input string, state State) (Stroke, bool) {
	entry := r.mapForID(id)
	if entry == nil {
		return Stroke{}, false
	}
	rule, ok := entry.physical[input]
	if !ok {
		return Stroke{}, false
	}
	value := rule.stroke(state)
	if state == StateShift && value == nil && rule.plain != nil {
		copy := *rule.plain
		if !copy.Suppress {
			copy.Modifiers |= modifiers["left_shift"]
		}
		value = &copy
	}
	if value == nil {
		return Stroke{}, false
	}
	return *value, true
}

func (r *Registry) ResolveText(id string, char rune) ([]Stroke, bool) {
	entry := r.mapForID(id)
	if entry == nil {
		return nil, false
	}
	strokes, ok := entry.text[char]
	return append([]Stroke(nil), strokes...), ok
}

func (r *Registry) mapForID(id string) *resolvedMap {
	if r == nil {
		return nil
	}
	if id == "" {
		id = "us-base"
	}
	return r.maps[id]
}
