# iLO-KVM keyboard-map authoring guide

This document accompanies an exported, known-good `german.json` reference map. It is intentionally detailed so that a human or an LLM can create another source-language map without changing Go code.

## Purpose and direction

Every selectable map translates the labels and modifier combinations of one local source keyboard into USB HID strokes that produce equivalent US keyboard characters on the remote system. The direction is always:

`source language keyboard -> en-US remote keyboard`

The built-in `Default` mode is physical pass-through and is not described by these language maps. A language map is a delta over the protected `us-base` map.

## Top-level fields

- `schemaVersion`: Must be the number `1`.
- `id`: Stable lowercase identifier using letters, digits, `.`, `_`, or `-`. It must not be `us-base`.
- `displayName`: Text shown in the Keyboard Layout menu.
- `sourceLocale`: BCP-47-style source locale such as `de-DE` or `fr-FR`.
- `targetLocale`: Must remain `en-US`.
- `extends`: Normally `us-base`.
- `selectable`: Optional boolean. Omit it for a user-selectable map.
- `physical`: Source-key overrides for plain, Shift, and AltGr states.
- `text`: Optional clipboard-character overrides. Unlisted characters inherit from `us-base`.

Unknown fields are rejected. Duplicate physical inputs and duplicate text characters are rejected.

## Physical rules

Each physical entry has an `input` and one or more state rules:

```json
{
  "input": "OEM_2",
  "plain": { "key": "DIGIT_3", "modifiers": ["left_shift"] },
  "shift": { "key": "APOSTROPHE" },
  "altGr": { "key": "BACKSLASH" }
}
```

State priority is AltGr, Shift, then plain. Ctrl, Alt, GUI, and other semantic shortcuts bypass language translation and retain normal shortcut behavior. An unlisted state inherits the base behavior. Use an explicit state when a source-layout combination must differ from that fallback.

Use `{ "suppress": true }` when the source key has no portable representation on a plain US target. Suppression cannot be combined with `key` or `modifiers`.

## Clipboard text rules

Text rules map exactly one Unicode character to one or more press/release strokes:

```json
{
  "char": "@",
  "strokes": [
    { "key": "DIGIT_2", "modifiers": ["left_shift"] }
  ]
}
```

Text strokes cannot use `suppress`. If a character is not defined by the selected map or its base, iLO-KVM counts and skips it. Multi-stroke sequences are allowed, but they must remain operating-system independent. Do not invent Windows Alt codes, Linux Compose sequences, or application-specific shortcuts for a generic map.

## Allowed physical input names

Letters and digits: `A` through `Z`, and `0` through `9`.

Editing and navigation: `ENTER`, `ESCAPE`, `BACKSPACE`, `TAB`, `SPACE`, `INSERT`, `HOME`, `PAGE_UP`, `DELETE`, `END`, `PAGE_DOWN`, `RIGHT`, `LEFT`, `DOWN`, `UP`.

Function and lock keys: `CAPS_LOCK`, `F1` through `F12`, `PRINT_SCREEN`, `SCROLL_LOCK`, `PAUSE`, `NUM_LOCK`.

Source-layout punctuation keys: `OEM_MINUS`, `OEM_PLUS`, `OEM_1`, `OEM_2`, `OEM_3`, `OEM_4`, `OEM_5`, `OEM_6`, `OEM_7`, `OEM_COMMA`, `OEM_PERIOD`, `OEM_102`.

Keypad and application keys: `KEYPAD_DIVIDE`, `KEYPAD_MULTIPLY`, `KEYPAD_SUBTRACT`, `KEYPAD_ADD`, `KEYPAD_0` through `KEYPAD_9`, `KEYPAD_DECIMAL`, `APPLICATION`.

If Windows exposes a key that has no friendly name in this catalog, use its authoritative virtual-key value as `VK_0xNN`, where `NN` is exactly two hexadecimal digits. For example, Windows `VK_F13` is `VK_0x7C`. Generic VK identifiers make new input keys configurable without a iLO-KVM code change. Prefer a friendly name whenever one exists.

## Allowed HID output names

Letters: `A` through `Z`.

Number row: `DIGIT_0` through `DIGIT_9`.

Punctuation: `MINUS`, `EQUAL`, `LEFT_BRACKET`, `RIGHT_BRACKET`, `BACKSLASH`, `SEMICOLON`, `APOSTROPHE`, `GRAVE`, `COMMA`, `PERIOD`, `SLASH`, `NON_US_BACKSLASH`.

All editing, navigation, function, lock, keypad, and application names listed above are also valid HID output names where applicable.

If a required keyboard usage has no friendly HID name, use the authoritative USB HID Keyboard/Keypad usage as `HID_0xNN`, where `NN` is exactly two hexadecimal digits. For example, the F13 keyboard usage is `HID_0x68`. Do not use `HID_0x00` or modifier usages `HID_0xE0` through `HID_0xE7`; modifiers belong in the `modifiers` array.

Never place bare numeric values in a map and never guess a VK or HID value. Generic tokens are an advanced compatibility mechanism and must be taken from authoritative Microsoft virtual-key and USB HID Keyboard/Keypad documentation.

## Allowed modifiers

`left_ctrl`, `left_shift`, `left_alt`, `left_gui`, `right_ctrl`, `right_shift`, `right_alt`, `right_gui`.

Do not repeat a modifier in one stroke.

## Loading, overrides, and validation

Place a new JSON file directly in the `keyboard-maps` directory next to the executable. Subdirectories are documentation-only and are not scanned. Maps load once at application startup.

A valid external map whose ID is `german` completely replaces the built-in German map. If the external replacement is invalid, iLO-KVM reports the error and retains the built-in map. The protected `us-base` ID cannot be overridden. Two external files with the same ID are both rejected.

Before installing a generated map, verify:

1. The JSON parses without comments or trailing commas.
2. `schemaVersion` is `1` and `targetLocale` is `en-US`.
3. The ID is unique, lowercase, and is not `us-base`.
4. Every input, HID key, and modifier is taken from the catalogs above, or uses an authoritative `VK_0xNN`/`HID_0xNN` token.
5. Every source-layout difference is represented under the correct state.
6. Unsupported combinations use `suppress` instead of guessed HID codes.
7. Clipboard entries contain exactly one Unicode character each.
8. The application is restarted and the new menu entry is exercised against a safe test target.

## French map skeleton

This is metadata only. It deliberately contains no guessed French key assignments:

```json
{
  "schemaVersion": 1,
  "id": "french",
  "displayName": "French",
  "sourceLocale": "fr-FR",
  "targetLocale": "en-US",
  "extends": "us-base",
  "physical": [],
  "text": []
}
```

## Copy-and-paste LLM prompt

Create a complete iLO-KVM keyboard-map JSON file for the requested source keyboard layout by using the attached exported German JSON as the structural reference and this guide as the authoritative schema.

Requirements:

- Keep `schemaVersion` exactly `1`.
- Choose a new lowercase `id`, a clear `displayName`, and the correct `sourceLocale`.
- Keep `targetLocale` exactly `en-US` and `extends` exactly `us-base`.
- Translate source-key labels and their plain, Shift, and AltGr states into equivalent US HID strokes.
- Prefer the friendly physical-input names, HID-output names, and modifier names listed in this guide.
- If a required key is missing from the friendly catalogs, use an authoritative `VK_0xNN` input or `HID_0xNN` output token. Never emit bare numbers and never guess these values.
- Preserve normal Ctrl, Alt, GUI, and application shortcut behavior by defining only true source-layout deviations.
- Mark combinations that cannot be represented on a plain US target with `{ "suppress": true }`.
- Add clipboard text rules only when they are portable and operating-system independent.
- Do not invent mappings. If the source layout is uncertain, identify the uncertainty before producing the final file and use an authoritative layout specification.
- Return one complete valid JSON document with no comments, no trailing commas, no Markdown code fence, and no explanatory text around it.
