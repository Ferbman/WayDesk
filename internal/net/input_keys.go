package net

// WebCodeToLinux maps HTML KeyboardEvent.code strings to linux input/event keycodes.
// A minimal mapping for demonstration of full typing capabilities.
var WebCodeToLinux = map[string]int{
	"Escape": 1,
	"Digit1": 2, "Digit2": 3, "Digit3": 4, "Digit4": 5, "Digit5": 6, "Digit6": 7, "Digit7": 8, "Digit8": 9, "Digit9": 10, "Digit0": 11,
	"Minus": 12, "Equal": 13, "Backspace": 14, "Tab": 15,
	"KeyQ": 16, "KeyW": 17, "KeyE": 18, "KeyR": 19, "KeyT": 20, "KeyY": 21, "KeyU": 22, "KeyI": 23, "KeyO": 24, "KeyP": 25,
	"BracketLeft": 26, "BracketRight": 27, "Enter": 28, "ControlLeft": 29,
	"KeyA": 30, "KeyS": 31, "KeyD": 32, "KeyF": 33, "KeyG": 34, "KeyH": 35, "KeyJ": 36, "KeyK": 37, "KeyL": 38,
	"Semicolon": 39, "Quote": 40, "Backquote": 41, "ShiftLeft": 42, "Backslash": 43,
	"KeyZ": 44, "KeyX": 45, "KeyC": 46, "KeyV": 47, "KeyB": 48, "KeyN": 49, "KeyM": 50,
	"Comma": 51, "Period": 52, "Slash": 53, "ShiftRight": 54, "AltLeft": 56, "Space": 57,
	"CapsLock": 58, "F1": 59, "F2": 60, "F3": 61, "F4": 62, "F5": 63, "F6": 64, "F7": 65, "F8": 66, "F9": 67, "F10": 68,
	"ArrowUp": 103, "ArrowLeft": 105, "ArrowRight": 106, "ArrowDown": 108,
}
