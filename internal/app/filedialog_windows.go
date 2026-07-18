//go:build windows

package app

import "github.com/lxn/walk"

func chooseISOFile(initialPath string) (string, bool, error) {
	dlg := walk.FileDialog{
		Title:    "ISO mounten",
		FilePath: initialPath,
		Filter:   "ISO Images (*.iso)|*.iso|All Files (*.*)|*.*",
	}
	ok, err := dlg.ShowOpen(nil)
	if err != nil || !ok {
		return "", ok, err
	}
	return dlg.FilePath, true, nil
}

func chooseKeyboardMapExport(initialPath string) (string, bool, error) {
	dlg := walk.FileDialog{
		Title:    "Export built-in German keyboard map",
		FilePath: initialPath,
		Filter:   "JSON keyboard maps (*.json)|*.json|All Files (*.*)|*.*",
	}
	ok, err := dlg.ShowSave(nil)
	if err != nil || !ok {
		return "", ok, err
	}
	return dlg.FilePath, true, nil
}
