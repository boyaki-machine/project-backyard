package ulidgen

import "testing"

// DbDesign.md 4.2：ULID は Crockford Base32 の26文字固定。
func TestNew(t *testing.T) {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

	seen := make(map[string]bool, 100)
	for range 100 {
		id := New()
		if len(id) != 26 {
			t.Fatalf("len(%q) = %d, want 26", id, len(id))
		}
		for _, r := range id {
			if !containsRune(crockford, r) {
				t.Fatalf("%q に Crockford Base32 外の文字 %q が含まれる", id, r)
			}
		}
		if seen[id] {
			t.Fatalf("ULID が重複した: %q", id)
		}
		seen[id] = true
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
