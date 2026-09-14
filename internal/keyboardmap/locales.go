package keyboardmap

// The tables below were read out of the Windows keyboard layout DLLs. Do not
// edit single entries by hand: correct the measurement and take the whole
// table, or the two halves of a translation stop agreeing with each other.
//
// Every entry came from the layout DLLs themselves through
// MapVirtualKeyEx and ToUnicodeEx, for the en-US (0x0409) and German
// (0x0407) layouts, one key position at a time in the plain, shifted and
// AltGr states. The single exception is the Euro sign: ToUnicodeEx reports
// AltGr+E as U+00AC for a layout that is not the active one, which
// contradicts every German keyboard, so that one value is corrected.

// charsUS maps a HID usage to the characters the en-US layout puts on that
// position: plain, shifted, AltGr. A zero means the state produces no
// single character, which includes the dead keys.
var charsUS = map[byte][3]rune{
	4:   {'a', 'A', 0},  // A
	5:   {'b', 'B', 0},  // B
	6:   {'c', 'C', 0},  // C
	7:   {'d', 'D', 0},  // D
	8:   {'e', 'E', 0},  // E
	9:   {'f', 'F', 0},  // F
	10:  {'g', 'G', 0},  // G
	11:  {'h', 'H', 0},  // H
	12:  {'i', 'I', 0},  // I
	13:  {'j', 'J', 0},  // J
	14:  {'k', 'K', 0},  // K
	15:  {'l', 'L', 0},  // L
	16:  {'m', 'M', 0},  // M
	17:  {'n', 'N', 0},  // N
	18:  {'o', 'O', 0},  // O
	19:  {'p', 'P', 0},  // P
	20:  {'q', 'Q', 0},  // Q
	21:  {'r', 'R', 0},  // R
	22:  {'s', 'S', 0},  // S
	23:  {'t', 'T', 0},  // T
	24:  {'u', 'U', 0},  // U
	25:  {'v', 'V', 0},  // V
	26:  {'w', 'W', 0},  // W
	27:  {'x', 'X', 0},  // X
	28:  {'y', 'Y', 0},  // Y
	29:  {'z', 'Z', 0},  // Z
	30:  {'1', '!', 0},  // DIGIT_1
	31:  {'2', '@', 0},  // DIGIT_2
	32:  {'3', '#', 0},  // DIGIT_3
	33:  {'4', '$', 0},  // DIGIT_4
	34:  {'5', '%', 0},  // DIGIT_5
	35:  {'6', '^', 0},  // DIGIT_6
	36:  {'7', '&', 0},  // DIGIT_7
	37:  {'8', '*', 0},  // DIGIT_8
	38:  {'9', '(', 0},  // DIGIT_9
	39:  {'0', ')', 0},  // DIGIT_0
	45:  {'-', '_', 0},  // MINUS
	46:  {'=', '+', 0},  // EQUAL
	47:  {'[', '{', 0},  // LEFT_BRACKET
	48:  {']', '}', 0},  // RIGHT_BRACKET
	49:  {'\\', '|', 0}, // BACKSLASH
	51:  {';', ':', 0},  // SEMICOLON
	52:  {'\'', '"', 0}, // APOSTROPHE
	53:  {'`', '~', 0},  // GRAVE
	54:  {',', '<', 0},  // COMMA
	55:  {'.', '>', 0},  // PERIOD
	56:  {'/', '?', 0},  // SLASH
	100: {'\\', '|', 0}, // NON_US_BACKSLASH
}

// charsDE maps a HID usage to the characters the de-DE layout puts on that
// position: plain, shifted, AltGr. A zero means the state produces no
// single character, which includes the dead keys.
var charsDE = map[byte][3]rune{
	4:   {'a', 'A', 0},         // A
	5:   {'b', 'B', 0},         // B
	6:   {'c', 'C', 0},         // C
	7:   {'d', 'D', 0},         // D
	8:   {'e', 'E', 0x20ac},    // E
	9:   {'f', 'F', 0},         // F
	10:  {'g', 'G', 0},         // G
	11:  {'h', 'H', 0},         // H
	12:  {'i', 'I', 0},         // I
	13:  {'j', 'J', 0},         // J
	14:  {'k', 'K', 0},         // K
	15:  {'l', 'L', 0},         // L
	16:  {'m', 'M', 0x00b5},    // M
	17:  {'n', 'N', 0},         // N
	18:  {'o', 'O', 0},         // O
	19:  {'p', 'P', 0},         // P
	20:  {'q', 'Q', '@'},       // Q
	21:  {'r', 'R', 0},         // R
	22:  {'s', 'S', 0},         // S
	23:  {'t', 'T', 0},         // T
	24:  {'u', 'U', 0},         // U
	25:  {'v', 'V', 0},         // V
	26:  {'w', 'W', 0},         // W
	27:  {'x', 'X', 0},         // X
	28:  {'z', 'Z', 0},         // Y
	29:  {'y', 'Y', 0},         // Z
	30:  {'1', '!', 0},         // DIGIT_1
	31:  {'2', '"', 0x00b2},    // DIGIT_2
	32:  {'3', 0x00a7, 0x00b3}, // DIGIT_3
	33:  {'4', '$', 0},         // DIGIT_4
	34:  {'5', '%', 0},         // DIGIT_5
	35:  {'6', '&', 0},         // DIGIT_6
	36:  {'7', '/', '{'},       // DIGIT_7
	37:  {'8', '(', '['},       // DIGIT_8
	38:  {'9', ')', ']'},       // DIGIT_9
	39:  {'0', '=', '}'},       // DIGIT_0
	45:  {0x00df, '?', '\\'},   // MINUS
	46:  {0, 0, 0},             // EQUAL
	47:  {0x00fc, 0x00dc, 0},   // LEFT_BRACKET
	48:  {'+', '*', '~'},       // RIGHT_BRACKET
	49:  {'#', '\'', 0},        // BACKSLASH
	51:  {0x00f6, 0x00d6, 0},   // SEMICOLON
	52:  {0x00e4, 0x00c4, 0},   // APOSTROPHE
	53:  {0, 0x00b0, 0},        // GRAVE
	54:  {',', ';', 0},         // COMMA
	55:  {'.', ':', 0},         // PERIOD
	56:  {'-', '_', 0},         // SLASH
	100: {'<', '>', '|'},       // NON_US_BACKSLASH
}

// encodeUS maps a character to the keystroke that produces it on a
// remote running the en-US layout.
var encodeUS = map[rune]Stroke{
	'!':  {Key: 30, Modifiers: modShift},
	'"':  {Key: 52, Modifiers: modShift},
	'#':  {Key: 32, Modifiers: modShift},
	'$':  {Key: 33, Modifiers: modShift},
	'%':  {Key: 34, Modifiers: modShift},
	'&':  {Key: 36, Modifiers: modShift},
	'\'': {Key: 52, Modifiers: 0},
	'(':  {Key: 38, Modifiers: modShift},
	')':  {Key: 39, Modifiers: modShift},
	'*':  {Key: 37, Modifiers: modShift},
	'+':  {Key: 46, Modifiers: modShift},
	',':  {Key: 54, Modifiers: 0},
	'-':  {Key: 45, Modifiers: 0},
	'.':  {Key: 55, Modifiers: 0},
	'/':  {Key: 56, Modifiers: 0},
	'0':  {Key: 39, Modifiers: 0},
	'1':  {Key: 30, Modifiers: 0},
	'2':  {Key: 31, Modifiers: 0},
	'3':  {Key: 32, Modifiers: 0},
	'4':  {Key: 33, Modifiers: 0},
	'5':  {Key: 34, Modifiers: 0},
	'6':  {Key: 35, Modifiers: 0},
	'7':  {Key: 36, Modifiers: 0},
	'8':  {Key: 37, Modifiers: 0},
	'9':  {Key: 38, Modifiers: 0},
	':':  {Key: 51, Modifiers: modShift},
	';':  {Key: 51, Modifiers: 0},
	'<':  {Key: 54, Modifiers: modShift},
	'=':  {Key: 46, Modifiers: 0},
	'>':  {Key: 55, Modifiers: modShift},
	'?':  {Key: 56, Modifiers: modShift},
	'@':  {Key: 31, Modifiers: modShift},
	'A':  {Key: 4, Modifiers: modShift},
	'B':  {Key: 5, Modifiers: modShift},
	'C':  {Key: 6, Modifiers: modShift},
	'D':  {Key: 7, Modifiers: modShift},
	'E':  {Key: 8, Modifiers: modShift},
	'F':  {Key: 9, Modifiers: modShift},
	'G':  {Key: 10, Modifiers: modShift},
	'H':  {Key: 11, Modifiers: modShift},
	'I':  {Key: 12, Modifiers: modShift},
	'J':  {Key: 13, Modifiers: modShift},
	'K':  {Key: 14, Modifiers: modShift},
	'L':  {Key: 15, Modifiers: modShift},
	'M':  {Key: 16, Modifiers: modShift},
	'N':  {Key: 17, Modifiers: modShift},
	'O':  {Key: 18, Modifiers: modShift},
	'P':  {Key: 19, Modifiers: modShift},
	'Q':  {Key: 20, Modifiers: modShift},
	'R':  {Key: 21, Modifiers: modShift},
	'S':  {Key: 22, Modifiers: modShift},
	'T':  {Key: 23, Modifiers: modShift},
	'U':  {Key: 24, Modifiers: modShift},
	'V':  {Key: 25, Modifiers: modShift},
	'W':  {Key: 26, Modifiers: modShift},
	'X':  {Key: 27, Modifiers: modShift},
	'Y':  {Key: 28, Modifiers: modShift},
	'Z':  {Key: 29, Modifiers: modShift},
	'[':  {Key: 47, Modifiers: 0},
	'\\': {Key: 49, Modifiers: 0},
	']':  {Key: 48, Modifiers: 0},
	'^':  {Key: 35, Modifiers: modShift},
	'_':  {Key: 45, Modifiers: modShift},
	'`':  {Key: 53, Modifiers: 0},
	'a':  {Key: 4, Modifiers: 0},
	'b':  {Key: 5, Modifiers: 0},
	'c':  {Key: 6, Modifiers: 0},
	'd':  {Key: 7, Modifiers: 0},
	'e':  {Key: 8, Modifiers: 0},
	'f':  {Key: 9, Modifiers: 0},
	'g':  {Key: 10, Modifiers: 0},
	'h':  {Key: 11, Modifiers: 0},
	'i':  {Key: 12, Modifiers: 0},
	'j':  {Key: 13, Modifiers: 0},
	'k':  {Key: 14, Modifiers: 0},
	'l':  {Key: 15, Modifiers: 0},
	'm':  {Key: 16, Modifiers: 0},
	'n':  {Key: 17, Modifiers: 0},
	'o':  {Key: 18, Modifiers: 0},
	'p':  {Key: 19, Modifiers: 0},
	'q':  {Key: 20, Modifiers: 0},
	'r':  {Key: 21, Modifiers: 0},
	's':  {Key: 22, Modifiers: 0},
	't':  {Key: 23, Modifiers: 0},
	'u':  {Key: 24, Modifiers: 0},
	'v':  {Key: 25, Modifiers: 0},
	'w':  {Key: 26, Modifiers: 0},
	'x':  {Key: 27, Modifiers: 0},
	'y':  {Key: 28, Modifiers: 0},
	'z':  {Key: 29, Modifiers: 0},
	'{':  {Key: 47, Modifiers: modShift},
	'|':  {Key: 49, Modifiers: modShift},
	'}':  {Key: 48, Modifiers: modShift},
	'~':  {Key: 53, Modifiers: modShift},
	' ':  {Key: 44},
}

// encodeDE maps a character to the keystroke that produces it on a
// remote running the de-DE layout.
var encodeDE = map[rune]Stroke{
	'!':    {Key: 30, Modifiers: modShift},
	'"':    {Key: 31, Modifiers: modShift},
	'#':    {Key: 49, Modifiers: 0},
	'$':    {Key: 33, Modifiers: modShift},
	'%':    {Key: 34, Modifiers: modShift},
	'&':    {Key: 35, Modifiers: modShift},
	'\'':   {Key: 49, Modifiers: modShift},
	'(':    {Key: 37, Modifiers: modShift},
	')':    {Key: 38, Modifiers: modShift},
	'*':    {Key: 48, Modifiers: modShift},
	'+':    {Key: 48, Modifiers: 0},
	',':    {Key: 54, Modifiers: 0},
	'-':    {Key: 56, Modifiers: 0},
	'.':    {Key: 55, Modifiers: 0},
	'/':    {Key: 36, Modifiers: modShift},
	'0':    {Key: 39, Modifiers: 0},
	'1':    {Key: 30, Modifiers: 0},
	'2':    {Key: 31, Modifiers: 0},
	'3':    {Key: 32, Modifiers: 0},
	'4':    {Key: 33, Modifiers: 0},
	'5':    {Key: 34, Modifiers: 0},
	'6':    {Key: 35, Modifiers: 0},
	'7':    {Key: 36, Modifiers: 0},
	'8':    {Key: 37, Modifiers: 0},
	'9':    {Key: 38, Modifiers: 0},
	':':    {Key: 55, Modifiers: modShift},
	';':    {Key: 54, Modifiers: modShift},
	'<':    {Key: 100, Modifiers: 0},
	'=':    {Key: 39, Modifiers: modShift},
	'>':    {Key: 100, Modifiers: modShift},
	'?':    {Key: 45, Modifiers: modShift},
	'@':    {Key: 20, Modifiers: modAltGr},
	'A':    {Key: 4, Modifiers: modShift},
	'B':    {Key: 5, Modifiers: modShift},
	'C':    {Key: 6, Modifiers: modShift},
	'D':    {Key: 7, Modifiers: modShift},
	'E':    {Key: 8, Modifiers: modShift},
	'F':    {Key: 9, Modifiers: modShift},
	'G':    {Key: 10, Modifiers: modShift},
	'H':    {Key: 11, Modifiers: modShift},
	'I':    {Key: 12, Modifiers: modShift},
	'J':    {Key: 13, Modifiers: modShift},
	'K':    {Key: 14, Modifiers: modShift},
	'L':    {Key: 15, Modifiers: modShift},
	'M':    {Key: 16, Modifiers: modShift},
	'N':    {Key: 17, Modifiers: modShift},
	'O':    {Key: 18, Modifiers: modShift},
	'P':    {Key: 19, Modifiers: modShift},
	'Q':    {Key: 20, Modifiers: modShift},
	'R':    {Key: 21, Modifiers: modShift},
	'S':    {Key: 22, Modifiers: modShift},
	'T':    {Key: 23, Modifiers: modShift},
	'U':    {Key: 24, Modifiers: modShift},
	'V':    {Key: 25, Modifiers: modShift},
	'W':    {Key: 26, Modifiers: modShift},
	'X':    {Key: 27, Modifiers: modShift},
	'Y':    {Key: 29, Modifiers: modShift},
	'Z':    {Key: 28, Modifiers: modShift},
	'[':    {Key: 37, Modifiers: modAltGr},
	'\\':   {Key: 45, Modifiers: modAltGr},
	']':    {Key: 38, Modifiers: modAltGr},
	'_':    {Key: 56, Modifiers: modShift},
	'a':    {Key: 4, Modifiers: 0},
	'b':    {Key: 5, Modifiers: 0},
	'c':    {Key: 6, Modifiers: 0},
	'd':    {Key: 7, Modifiers: 0},
	'e':    {Key: 8, Modifiers: 0},
	'f':    {Key: 9, Modifiers: 0},
	'g':    {Key: 10, Modifiers: 0},
	'h':    {Key: 11, Modifiers: 0},
	'i':    {Key: 12, Modifiers: 0},
	'j':    {Key: 13, Modifiers: 0},
	'k':    {Key: 14, Modifiers: 0},
	'l':    {Key: 15, Modifiers: 0},
	'm':    {Key: 16, Modifiers: 0},
	'n':    {Key: 17, Modifiers: 0},
	'o':    {Key: 18, Modifiers: 0},
	'p':    {Key: 19, Modifiers: 0},
	'q':    {Key: 20, Modifiers: 0},
	'r':    {Key: 21, Modifiers: 0},
	's':    {Key: 22, Modifiers: 0},
	't':    {Key: 23, Modifiers: 0},
	'u':    {Key: 24, Modifiers: 0},
	'v':    {Key: 25, Modifiers: 0},
	'w':    {Key: 26, Modifiers: 0},
	'x':    {Key: 27, Modifiers: 0},
	'y':    {Key: 29, Modifiers: 0},
	'z':    {Key: 28, Modifiers: 0},
	'{':    {Key: 36, Modifiers: modAltGr},
	'|':    {Key: 100, Modifiers: modAltGr},
	'}':    {Key: 39, Modifiers: modAltGr},
	'~':    {Key: 48, Modifiers: modAltGr},
	0x00a7: {Key: 32, Modifiers: modShift},
	0x00b0: {Key: 53, Modifiers: modShift},
	0x00b2: {Key: 31, Modifiers: modAltGr},
	0x00b3: {Key: 32, Modifiers: modAltGr},
	0x00b5: {Key: 16, Modifiers: modAltGr},
	0x00c4: {Key: 52, Modifiers: modShift},
	0x00d6: {Key: 51, Modifiers: modShift},
	0x00dc: {Key: 47, Modifiers: modShift},
	0x00df: {Key: 45, Modifiers: 0},
	0x00e4: {Key: 52, Modifiers: 0},
	0x00f6: {Key: 51, Modifiers: 0},
	0x00fc: {Key: 47, Modifiers: 0},
	0x20ac: {Key: 8, Modifiers: modAltGr},
	' ':    {Key: 44},
}

// vkUSUsage maps a Windows virtual key of the us layout back to the key
// position it sits on.
var vkUSUsage = map[uint32]byte{
	48:  39,  // DIGIT_0
	49:  30,  // DIGIT_1
	50:  31,  // DIGIT_2
	51:  32,  // DIGIT_3
	52:  33,  // DIGIT_4
	53:  34,  // DIGIT_5
	54:  35,  // DIGIT_6
	55:  36,  // DIGIT_7
	56:  37,  // DIGIT_8
	57:  38,  // DIGIT_9
	65:  4,   // A
	66:  5,   // B
	67:  6,   // C
	68:  7,   // D
	69:  8,   // E
	70:  9,   // F
	71:  10,  // G
	72:  11,  // H
	73:  12,  // I
	74:  13,  // J
	75:  14,  // K
	76:  15,  // L
	77:  16,  // M
	78:  17,  // N
	79:  18,  // O
	80:  19,  // P
	81:  20,  // Q
	82:  21,  // R
	83:  22,  // S
	84:  23,  // T
	85:  24,  // U
	86:  25,  // V
	87:  26,  // W
	88:  27,  // X
	89:  28,  // Y
	90:  29,  // Z
	186: 51,  // SEMICOLON
	187: 46,  // EQUAL
	188: 54,  // COMMA
	189: 45,  // MINUS
	190: 55,  // PERIOD
	191: 56,  // SLASH
	192: 53,  // GRAVE
	219: 47,  // LEFT_BRACKET
	220: 49,  // BACKSLASH
	221: 48,  // RIGHT_BRACKET
	222: 52,  // APOSTROPHE
	226: 100, // NON_US_BACKSLASH
}

// vkDEUsage maps a Windows virtual key of the de layout back to the key
// position it sits on.
var vkDEUsage = map[uint32]byte{
	48:  39,  // DIGIT_0
	49:  30,  // DIGIT_1
	50:  31,  // DIGIT_2
	51:  32,  // DIGIT_3
	52:  33,  // DIGIT_4
	53:  34,  // DIGIT_5
	54:  35,  // DIGIT_6
	55:  36,  // DIGIT_7
	56:  37,  // DIGIT_8
	57:  38,  // DIGIT_9
	65:  4,   // A
	66:  5,   // B
	67:  6,   // C
	68:  7,   // D
	69:  8,   // E
	70:  9,   // F
	71:  10,  // G
	72:  11,  // H
	73:  12,  // I
	74:  13,  // J
	75:  14,  // K
	76:  15,  // L
	77:  16,  // M
	78:  17,  // N
	79:  18,  // O
	80:  19,  // P
	81:  20,  // Q
	82:  21,  // R
	83:  22,  // S
	84:  23,  // T
	85:  24,  // U
	86:  25,  // V
	87:  26,  // W
	88:  27,  // X
	89:  29,  // Z
	90:  28,  // Y
	186: 47,  // LEFT_BRACKET
	187: 48,  // RIGHT_BRACKET
	188: 54,  // COMMA
	189: 56,  // SLASH
	190: 55,  // PERIOD
	191: 49,  // BACKSLASH
	192: 51,  // SEMICOLON
	219: 45,  // MINUS
	220: 53,  // GRAVE
	221: 46,  // EQUAL
	222: 52,  // APOSTROPHE
	226: 100, // NON_US_BACKSLASH
}
