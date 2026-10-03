package discord

import "testing"

// Sscanf would accept "42abc" and stop at the first non-digit, quietly turning
// a malformed ID into a valid-looking one.
func TestParseIDRejectsAnythingButDigits(t *testing.T) {
	if got := parseID("b_42"); got != 0 {
		t.Errorf("parseID(%q) = %d, want 0", "b_42", got)
	}
	if got := parseID("42"); got != 42 {
		t.Errorf("parseID(%q) = %d, want 42", "42", got)
	}
}
