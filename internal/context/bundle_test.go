package context

import (
	"testing"
)

func TestSanitizeUntrusted(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"normal", "hello world", "hello world"},
		{"null bytes", "hello\x00world", "helloworld"},
		{"ansi escape", "hello\x1b[31mred\x1b[0m", "hellored"},
		{"ansi bold", "\x1b[1mbold\x1b[0m text", "bold text"},
		{"multiple nulls", "\x00\x00test\x00", "test"},
		{"unicode", "café", "café"},
		{"mixed", "\x00hello\x1b[32mgreen\x00\x1b[0m", "hellogreen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeUntrusted(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeUntrusted(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
