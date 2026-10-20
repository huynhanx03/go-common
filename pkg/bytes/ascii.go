package bytes

// IsDigit reports whether c is an ASCII decimal digit ('0' through '9').
func IsDigit(c byte) bool {
	return c-'0' <= '9'-'0'
}

// IsLetter reports whether c is an ASCII letter ('A' through 'Z' or 'a' through 'z').
func IsLetter(c byte) bool {
	c |= 0x20
	return c-'a' <= 'z'-'a'
}

// IsAlnum reports whether c is an ASCII letter or decimal digit.
func IsAlnum(c byte) bool {
	return IsDigit(c) || IsLetter(c)
}
