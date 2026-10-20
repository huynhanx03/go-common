package bytes

import "testing"

func TestASCIIClassification(t *testing.T) {
	for value := 0; value <= 255; value++ {
		c := byte(value)
		digit := c >= '0' && c <= '9'
		letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'

		if got := IsDigit(c); got != digit {
			t.Errorf("IsDigit(%#02x) = %t, want %t", c, got, digit)
		}
		if got := IsLetter(c); got != letter {
			t.Errorf("IsLetter(%#02x) = %t, want %t", c, got, letter)
		}
		if got := IsAlnum(c); got != (digit || letter) {
			t.Errorf("IsAlnum(%#02x) = %t, want %t", c, got, digit || letter)
		}
	}
}
