package main

import (
	"strings"
	"testing"
)

func TestIsValidProjectName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "single character",
			value: "a",
			valid: true,
		},
		{
			name:  "simple dns label",
			value: "demo-app",
			valid: true,
		},
		{
			name:  "interior double hyphen remains valid",
			value: "demo--app",
			valid: true,
		},
		{
			name:  "max label length",
			value: strings.Repeat("a", 63),
			valid: true,
		},
		{
			name:  "empty",
			value: "",
			valid: false,
		},
		{
			name:  "leading hyphen",
			value: "-demo",
			valid: false,
		},
		{
			name:  "trailing hyphen",
			value: "demo-",
			valid: false,
		},
		{
			name:  "uppercase",
			value: "Demo",
			valid: false,
		},
		{
			name:  "underscore",
			value: "demo_app",
			valid: false,
		},
		{
			name:  "dot",
			value: "demo.app",
			valid: false,
		},
		{
			name:  "too long",
			value: strings.Repeat("a", 64),
			valid: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := isValidProjectName(test.value); got != test.valid {
				t.Fatalf("expected %q validity %t, got %t", test.value, test.valid, got)
			}
		})
	}
}
