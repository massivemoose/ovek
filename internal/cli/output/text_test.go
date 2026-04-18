package output

import (
	"strings"
	"testing"
)

func TestWriteKeyValuesAlignsOutput(t *testing.T) {
	var body strings.Builder
	WriteKeyValues(&body, [][2]string{
		{"Host", "http://brain.localhost"},
		{"Auth", "yes"},
	})

	text := body.String()
	for _, fragment := range []string{"Host", "Auth", "http://brain.localhost"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, text)
		}
	}
}

func TestWriteTableIncludesHeadersAndRows(t *testing.T) {
	var body strings.Builder
	WriteTable(&body, []string{"Name", "Status"}, [][]string{{"demo-app", "running"}})

	text := body.String()
	for _, fragment := range []string{"Name", "Status", "demo-app", "running"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected output to contain %q, got %q", fragment, text)
		}
	}
}
