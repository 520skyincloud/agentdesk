package utils

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

var windows1252Reverse = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85,
	'†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89, 'Š': 0x8a,
	'‹': 0x8b, 'Œ': 0x8c, 'Ž': 0x8e, '‘': 0x91, '’': 0x92,
	'“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97,
	'˜': 0x98, '™': 0x99, 'š': 0x9a, '›': 0x9b, 'œ': 0x9c,
	'ž': 0x9e, 'Ÿ': 0x9f,
}

// RepairMojibakeText fixes UTF-8 Chinese text that was once decoded as Windows-1252.
func RepairMojibakeText(value string) string {
	if value == "" || !looksLikeUTF8Mojibake(value) {
		return value
	}
	repaired := decodeWindows1252AsUTF8(value)
	if repaired == "" || repaired == value {
		return value
	}
	if scoreReadableCJK(repaired) > scoreReadableCJK(value) {
		return repaired
	}
	return value
}

func looksLikeUTF8Mojibake(value string) bool {
	for _, r := range value {
		if (r >= 0x80 && r <= 0x9f) || strings.ContainsRune("ÃÂÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿ¼½¾¥œ€", r) {
			return true
		}
	}
	return false
}

func decodeWindows1252AsUTF8(value string) string {
	bytes := make([]byte, 0, len(value))
	for _, r := range value {
		if b, ok := windows1252Reverse[r]; ok {
			bytes = append(bytes, b)
			continue
		}
		if r <= 0xff {
			bytes = append(bytes, byte(r))
			continue
		}
		bytes = append(bytes, []byte(string(r))...)
	}
	if !utf8.Valid(bytes) {
		return value
	}
	return string(bytes)
}

func scoreReadableCJK(value string) int {
	score := 0
	for _, r := range value {
		switch {
		case r >= 0x3400 && r <= 0x9fff:
			score += 3
		case r == utf8.RuneError:
			score -= 4
		case r >= 0x80 && r <= 0x9f:
			score--
		case unicode.IsControl(r):
			score -= 2
		case strings.ContainsRune("ÃÂÄÅÆÇÈÉÊËÌÍÎÏÐÑÒÓÔÕÖ×ØÙÚÛÜÝÞßàáâãäåæçèéêëìíîïðñòóôõöøùúûüýþÿ¼½¾¥œ€", r):
			score--
		}
	}
	return score
}
