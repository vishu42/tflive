package strval

import "testing"

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"first wins", []string{"alice", "bob"}, "alice"},
		{"skips leading empties", []string{"", "", "alice"}, "alice"},
		{"all empty", []string{"", ""}, ""},
		{"no values", nil, ""},
		{"whitespace is not empty", []string{"", " "}, " "},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := FirstNonEmpty(test.values...); got != test.want {
				t.Fatalf("FirstNonEmpty(%q) = %q, want %q", test.values, got, test.want)
			}
		})
	}
}

func TestSafeOpaque(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"identifier", "01H8XVQ3", true},
		{"punctuation is allowed", "store-id_1.2:3", true},
		{"empty", "", false},
		{"space", "has space", false},
		{"leading space", " leading", false},
		{"newline", "has\nnewline", false},
		{"tab", "has\ttab", false},
		{"carriage return", "has\rreturn", false},
		{"null byte", "has\x00null", false},
		{"non-breaking space", "has space", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := SafeOpaque(test.value); got != test.want {
				t.Fatalf("SafeOpaque(%q) = %t, want %t", test.value, got, test.want)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  string
		secret string
		want   string
	}{
		{"replaces the secret", "token abc leaked", "abc", "token [REDACTED] leaked"},
		{"replaces every occurrence", "abc and abc", "abc", "[REDACTED] and [REDACTED]"},
		{"absent secret", "nothing here", "abc", "nothing here"},
		{"empty secret changes nothing", "anything", "", "anything"},
		{"empty value", "", "abc", ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := Redact(test.value, test.secret); got != test.want {
				t.Fatalf("Redact(%q, %q) = %q, want %q", test.value, test.secret, got, test.want)
			}
		})
	}
}
