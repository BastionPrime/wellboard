package geodata

// validTag reports whether a category tag is a usable v2fly tag
// (A2): non-empty, no whitespace, printable ASCII without control
// characters. Case is preserved — v2fly tags are uppercase, but
// KnownGeositeTags serves the lowercased datalist and mihomo matches
// case-insensitively, so mixed case is accepted and passed through.
func validTag(tag string) bool {
	if tag == "" {
		return false
	}
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if c <= ' ' || c >= 0x7f {
			return false // whitespace, control, or non-ASCII
		}
	}
	return true
}
