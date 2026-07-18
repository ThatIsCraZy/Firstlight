package keyboardmap

import (
	"fmt"
	"strconv"
	"strings"
)

var inputKeys = map[string]bool{
	"A": true, "B": true, "C": true, "D": true, "E": true, "F": true,
	"G": true, "H": true, "I": true, "J": true, "K": true, "L": true,
	"M": true, "N": true, "O": true, "P": true, "Q": true, "R": true,
	"S": true, "T": true, "U": true, "V": true, "W": true, "X": true,
	"Y": true, "Z": true,
	"1": true, "2": true, "3": true, "4": true, "5": true,
	"6": true, "7": true, "8": true, "9": true, "0": true,
	"ENTER": true, "ESCAPE": true, "BACKSPACE": true, "TAB": true, "SPACE": true,
	"OEM_MINUS": true, "OEM_PLUS": true, "OEM_1": true, "OEM_2": true,
	"OEM_3": true, "OEM_4": true, "OEM_5": true, "OEM_6": true,
	"OEM_7": true, "OEM_COMMA": true, "OEM_PERIOD": true, "OEM_102": true,
	"CAPS_LOCK": true, "F1": true, "F2": true, "F3": true, "F4": true,
	"F5": true, "F6": true, "F7": true, "F8": true, "F9": true, "F10": true,
	"F11": true, "F12": true, "PRINT_SCREEN": true, "SCROLL_LOCK": true,
	"PAUSE": true, "INSERT": true, "HOME": true, "PAGE_UP": true,
	"DELETE": true, "END": true, "PAGE_DOWN": true, "RIGHT": true,
	"LEFT": true, "DOWN": true, "UP": true, "NUM_LOCK": true,
	"KEYPAD_DIVIDE": true, "KEYPAD_MULTIPLY": true, "KEYPAD_SUBTRACT": true,
	"KEYPAD_ADD": true, "KEYPAD_0": true, "KEYPAD_1": true, "KEYPAD_2": true,
	"KEYPAD_3": true, "KEYPAD_4": true, "KEYPAD_5": true, "KEYPAD_6": true,
	"KEYPAD_7": true, "KEYPAD_8": true, "KEYPAD_9": true,
	"KEYPAD_DECIMAL": true, "APPLICATION": true,
}

var hidKeys = map[string]byte{
	"A": 4, "B": 5, "C": 6, "D": 7, "E": 8, "F": 9,
	"G": 10, "H": 11, "I": 12, "J": 13, "K": 14, "L": 15,
	"M": 16, "N": 17, "O": 18, "P": 19, "Q": 20, "R": 21,
	"S": 22, "T": 23, "U": 24, "V": 25, "W": 26, "X": 27,
	"Y": 28, "Z": 29,
	"DIGIT_1": 30, "DIGIT_2": 31, "DIGIT_3": 32, "DIGIT_4": 33,
	"DIGIT_5": 34, "DIGIT_6": 35, "DIGIT_7": 36, "DIGIT_8": 37,
	"DIGIT_9": 38, "DIGIT_0": 39,
	"ENTER": 40, "ESCAPE": 41, "BACKSPACE": 42, "TAB": 43, "SPACE": 44,
	"MINUS": 45, "EQUAL": 46, "LEFT_BRACKET": 47, "RIGHT_BRACKET": 48,
	"BACKSLASH": 49, "SEMICOLON": 51, "APOSTROPHE": 52, "GRAVE": 53,
	"COMMA": 54, "PERIOD": 55, "SLASH": 56, "CAPS_LOCK": 57,
	"F1": 58, "F2": 59, "F3": 60, "F4": 61, "F5": 62, "F6": 63,
	"F7": 64, "F8": 65, "F9": 66, "F10": 67, "F11": 68, "F12": 69,
	"PRINT_SCREEN": 70, "SCROLL_LOCK": 71, "PAUSE": 72, "INSERT": 73,
	"HOME": 74, "PAGE_UP": 75, "DELETE": 76, "END": 77, "PAGE_DOWN": 78,
	"RIGHT": 79, "LEFT": 80, "DOWN": 81, "UP": 82, "NUM_LOCK": 83,
	"KEYPAD_DIVIDE": 84, "KEYPAD_MULTIPLY": 85, "KEYPAD_SUBTRACT": 86,
	"KEYPAD_ADD": 87, "KEYPAD_1": 89, "KEYPAD_2": 90, "KEYPAD_3": 91,
	"KEYPAD_4": 92, "KEYPAD_5": 93, "KEYPAD_6": 94, "KEYPAD_7": 95,
	"KEYPAD_8": 96, "KEYPAD_9": 97, "KEYPAD_0": 98, "KEYPAD_DECIMAL": 99,
	"NON_US_BACKSLASH": 100, "APPLICATION": 101,
}

var modifiers = map[string]byte{
	"left_ctrl": 1, "left_shift": 2, "left_alt": 4, "left_gui": 8,
	"right_ctrl": 16, "right_shift": 32, "right_alt": 64, "right_gui": 128,
}

// VKName returns the canonical JSON identifier for any Windows virtual-key value.
// It is the escape hatch for keys that do not have a friendly built-in alias.
func VKName(vk uint32) string {
	return fmt.Sprintf("VK_0x%02X", vk&0xff)
}

func validInputKey(name string) bool {
	if inputKeys[name] {
		return true
	}
	value, ok := parseHexKeyName(name, "VK_0x")
	return ok && value != 0
}

func resolveHIDKey(name string) (byte, bool) {
	if value, ok := hidKeys[name]; ok {
		return value, true
	}
	value, ok := parseHexKeyName(name, "HID_0x")
	if !ok || value == 0 || (value >= 0xe0 && value <= 0xe7) {
		return 0, false
	}
	return value, true
}

func parseHexKeyName(name, prefix string) (byte, bool) {
	if !strings.HasPrefix(name, prefix) || len(name) != len(prefix)+2 {
		return 0, false
	}
	value, err := strconv.ParseUint(name[len(prefix):], 16, 8)
	return byte(value), err == nil
}
