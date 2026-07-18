//go:build windows

package uiicon

import (
	"bytes"
	_ "embed"
	"image/png"

	"github.com/lxn/walk"
)

//go:embed app_icon.png
var iconPNG []byte

func Load() (*walk.Icon, error) {
	img, err := png.Decode(bytes.NewReader(iconPNG))
	if err != nil {
		return nil, err
	}
	return walk.NewIconFromImage(img)
}
