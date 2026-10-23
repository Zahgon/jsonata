package jsre

import "unicode/utf8"

// Units is the UTF-16 code units of a string held as WTF-8 (an unpaired surrogate keeps its
// three-byte generalised UTF-8 form).
func Units(s string) []uint16 {
	units := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		var r rune
		n := 3
		if c := s[i]; c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2]&0xC0 == 0x80 {
			r = rune(c&0x0F)<<12 | rune(s[i+1]&0x3F)<<6 | rune(s[i+2]&0x3F)
		} else {
			r, n = utf8.DecodeRuneInString(s[i:])
		}
		i += n
		if r >= 0x10000 {
			r -= 0x10000
			units = append(units, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			units = append(units, uint16(r))
		}
	}
	return units
}
