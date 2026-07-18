//go:build !windows

package app

import "errors"

func chooseISOFile(initialPath string) (string, bool, error) {
	_ = initialPath
	return "", false, errors.New("ISO file dialog is only implemented on Windows")
}

func chooseKeyboardMapExport(initialPath string) (string, bool, error) {
	_ = initialPath
	return "", false, errors.New("keyboard map export dialog is only implemented on Windows")
}
