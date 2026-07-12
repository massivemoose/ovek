package main

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestSecretScrubberWriterRedactsAtEveryWriteBoundary(t *testing.T) {
	const (
		secret = "secret-token"
		input  = "before secret-token after"
		want   = "before [redacted] after"
	)

	for split := 0; split <= len(input); split++ {
		t.Run(strconv.Itoa(split), func(t *testing.T) {
			var output bytes.Buffer
			writer := newSecretScrubber([]string{secret}).Writer(&output)

			writeSecretScrubberChunk(t, writer, input[:split])
			writeSecretScrubberChunk(t, writer, input[split:])
			if err := writer.Flush(); err != nil {
				t.Fatalf("Flush() error = %v", err)
			}
			if got := output.String(); got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}

func TestSecretScrubberWriterHandlesStreamingCases(t *testing.T) {
	tests := []struct {
		name    string
		secrets []string
		chunks  []string
		want    string
	}{
		{
			name:    "secret at EOF",
			secrets: []string{"secret-token"},
			chunks:  []string{"secret-token"},
			want:    "[redacted]",
		},
		{
			name:    "one byte at a time",
			secrets: []string{"secret-token"},
			chunks:  strings.Split("before secret-token after", ""),
			want:    "before [redacted] after",
		},
		{
			name:    "repeated and multiple secrets",
			secrets: []string{"alpha-secret", "beta-secret"},
			chunks:  []string{"alpha-", "secret beta-secret alpha-secret"},
			want:    "[redacted] [redacted] [redacted]",
		},
		{
			name:    "longest overlapping secret wins",
			secrets: []string{"secret", "secret-token"},
			chunks:  []string{"secret", "-token"},
			want:    "[redacted]",
		},
		{
			name:    "short secrets remain visible",
			secrets: []string{"abc"},
			chunks:  []string{"abc"},
			want:    "abc",
		},
		{
			name:    "no secrets passes through empty writes",
			secrets: nil,
			chunks:  []string{"", "plain text", ""},
			want:    "plain text",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writer := newSecretScrubber(test.secrets).Writer(&output)
			for _, chunk := range test.chunks {
				writeSecretScrubberChunk(t, writer, chunk)
			}
			if err := writer.Flush(); err != nil {
				t.Fatalf("Flush() error = %v", err)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("output = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSecretScrubberWriterPropagatesWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	writer := newSecretScrubber([]string{"secret-token"}).Writer(errorWriter{err: wantErr})

	payload := []byte("plain text long enough to emit")
	written, err := writer.Write(payload)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Write() error = %v, want %v", err, wantErr)
	}
	if written != len(payload) {
		t.Fatalf("Write() = %d bytes, want %d", written, len(payload))
	}
}

func TestSecretScrubberWriterPropagatesFlushError(t *testing.T) {
	wantErr := errors.New("flush failed")
	writer := newSecretScrubber([]string{"secret-token"}).Writer(errorWriter{err: wantErr})

	writeSecretScrubberChunk(t, writer, "sec")
	if err := writer.Flush(); !errors.Is(err, wantErr) {
		t.Fatalf("Flush() error = %v, want %v", err, wantErr)
	}
}

func writeSecretScrubberChunk(t *testing.T, writer io.Writer, chunk string) {
	t.Helper()

	written, err := io.WriteString(writer, chunk)
	if err != nil {
		t.Fatalf("Write(%q) error = %v", chunk, err)
	}
	if written != len(chunk) {
		t.Fatalf("Write(%q) = %d bytes, want %d", chunk, written, len(chunk))
	}
}

type errorWriter struct {
	err error
}

func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}
