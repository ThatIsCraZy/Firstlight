package keyboardmap

import "strings"

// The remote console never carries characters. It carries key positions, and
// the operating system on the far side turns a position into a character with
// whatever layout it has configured. Everything in this package until now
// assumed that layout is US, which is what BIOS, UEFI and most installers use.
// A running German Windows does not, and then the position that says "minus"
// on a US board arrives as the key that carries the sharp s.
//
// A Target names the layout the remote side applies, so a keystroke can be
// resolved to the character the user meant and then encoded again for that
// layout.
type Target struct {
	ID          string
	DisplayName string
	Locale      string
	encode      map[rune]Stroke
}

// Modifier bits of a HID keyboard report. AltGr is the right alt key; Windows
// synthesises the control half of the combination itself.
const (
	modShift = 1 << 1
	modAltGr = 1 << 6
)

var targetUS = &Target{
	ID:          "en-US",
	DisplayName: "US (en-US)",
	Locale:      "en-US",
	encode:      encodeUS,
}

var targetDE = &Target{
	ID:          "de-DE",
	DisplayName: "German (de-DE)",
	Locale:      "de-DE",
	encode:      encodeDE,
}

var targets = []*Target{targetUS, targetDE}

// DefaultTarget is the layout assumed when nothing is chosen. It stays US
// because that is what firmware, boot menus and installers present.
func DefaultTarget() *Target { return targetUS }

// Targets lists the selectable remote layouts, the default first.
func Targets() []*Target { return append([]*Target(nil), targets...) }

// TargetByID resolves a target by its identifier. An empty identifier is the
// default, so a configuration that never mentions a target keeps working.
func TargetByID(id string) (*Target, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return DefaultTarget(), true
	}
	for _, target := range targets {
		if strings.EqualFold(target.ID, id) {
			return target, true
		}
	}
	return nil, false
}

// IsDefault reports whether this target is the US layout, in which case the
// whole translation step is skipped and the pipeline behaves exactly as it did
// before targets existed.
func (t *Target) IsDefault() bool { return t == nil || t == targetUS }

// Stroke returns the keystroke that produces r on this layout.
func (t *Target) Stroke(r rune) (Stroke, bool) {
	if t == nil {
		return Stroke{}, false
	}
	stroke, ok := t.encode[r]
	return stroke, ok
}

// CharForVK reports the character a Windows virtual key produces on the named
// layout in the given state. The lookup goes through the key position, because
// that is what both halves of the translation have in common.
//
// An unknown locale, an unknown key or a state that produces no single
// character, which is what the dead keys do, all report false. The caller then
// falls back to the untranslated path rather than inventing a character.
func CharForVK(locale string, vk uint32, state State) (rune, bool) {
	var (
		usages map[uint32]byte
		chars  map[byte][3]rune
	)
	switch normaliseLocale(locale) {
	case "de-DE":
		usages, chars = vkDEUsage, charsDE
	case "en-US":
		usages, chars = vkUSUsage, charsUS
	default:
		return 0, false
	}
	usage, ok := usages[vk]
	if !ok {
		return 0, false
	}
	row, ok := chars[usage]
	if !ok {
		return 0, false
	}
	index := 0
	switch state {
	case StateShift:
		index = 1
	case StateAltGr:
		index = 2
	}
	if row[index] == 0 {
		return 0, false
	}
	return row[index], true
}

// normaliseLocale accepts the spellings that appear in map files.
func normaliseLocale(locale string) string {
	switch strings.ToLower(strings.TrimSpace(locale)) {
	case "de", "de-de", "de_de", "german":
		return "de-DE"
	case "", "en", "en-us", "en_us", "us":
		return "en-US"
	}
	return ""
}
