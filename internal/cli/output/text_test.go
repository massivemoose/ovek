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

func TestWriteAdaptiveTableKeepsTableWhenItFits(t *testing.T) {
	var body strings.Builder
	WriteAdaptiveTable(&body, []string{"Name", "Status"}, [][]string{{"demo-app", "running"}}, TableOptions{MaxWidth: 80})

	text := body.String()
	if !strings.Contains(text, "Name") || !strings.Contains(text, "demo-app") {
		t.Fatalf("expected table output, got %q", text)
	}
	if strings.Contains(text, "Name: demo-app") {
		t.Fatalf("expected wide table output, got record output %q", text)
	}
}

func TestWriteAdaptiveTableUsesRecordsWhenTooWide(t *testing.T) {
	var body strings.Builder
	WriteAdaptiveTable(
		&body,
		[]string{"Job", "Status", "Source"},
		[][]string{{"job_123", "failed", "ghcr.io/example/really-long-image-name:latest"}},
		TableOptions{MaxWidth: 32},
	)

	text := body.String()
	for _, fragment := range []string{"Job: job_123", "Status: failed", "Source: ghcr.io/example/really-long-image-name:latest"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected narrow output to contain %q, got %q", fragment, text)
		}
	}
	if strings.Contains(text, "Job      Status") {
		t.Fatalf("expected record output, got table output %q", text)
	}
}
