package keyboardmap

// MapFile is the public JSON schema for keyboard maps.
type MapFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id"`
	DisplayName   string         `json:"displayName"`
	SourceLocale  string         `json:"sourceLocale"`
	TargetLocale  string         `json:"targetLocale"`
	Extends       string         `json:"extends,omitempty"`
	Selectable    *bool          `json:"selectable,omitempty"`
	Physical      []PhysicalRule `json:"physical"`
	Text          []TextRule     `json:"text"`
}

type PhysicalRule struct {
	Input string      `json:"input"`
	Plain *StrokeSpec `json:"plain,omitempty"`
	Shift *StrokeSpec `json:"shift,omitempty"`
	AltGr *StrokeSpec `json:"altGr,omitempty"`
}

type TextRule struct {
	Char    string       `json:"char"`
	Strokes []StrokeSpec `json:"strokes"`
}

type StrokeSpec struct {
	Key       string   `json:"key,omitempty"`
	Modifiers []string `json:"modifiers,omitempty"`
	Suppress  bool     `json:"suppress,omitempty"`
}

type State string

const (
	StatePlain State = "plain"
	StateShift State = "shift"
	StateAltGr State = "altGr"
)

// Stroke is a validated keyboard action ready for an iLO HID report.
type Stroke struct {
	Key       byte
	Modifiers byte
	Suppress  bool
}

type MapInfo struct {
	ID           string
	DisplayName  string
	SourceLocale string
	TargetLocale string
}

type LoadResult struct {
	Registry  *Registry
	Directory string
	Warnings  []string
}
