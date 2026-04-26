package main

import (
	"io"
	"sort"
	"strings"
)

const minSecretScrubLength = 4

type secretScrubber struct {
	values []string
	maxLen int
}

func newSecretScrubber(values []string) secretScrubber {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		if len(value) < minSecretScrubLength {
			continue
		}
		unique[value] = struct{}{}
	}

	scrubber := secretScrubber{
		values: make([]string, 0, len(unique)),
	}
	for value := range unique {
		scrubber.values = append(scrubber.values, value)
		if len(value) > scrubber.maxLen {
			scrubber.maxLen = len(value)
		}
	}
	sort.Slice(scrubber.values, func(i, j int) bool {
		return len(scrubber.values[i]) > len(scrubber.values[j])
	})

	return scrubber
}

func (scrubber secretScrubber) Scrub(value string) string {
	for _, secret := range scrubber.values {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}

	return value
}

func (scrubber secretScrubber) Writer(writer io.Writer) *scrubbingWriter {
	return &scrubbingWriter{
		writer:   writer,
		scrubber: scrubber,
	}
}

type scrubbingWriter struct {
	writer   io.Writer
	scrubber secretScrubber
	tail     string
}

func (writer *scrubbingWriter) Write(payload []byte) (int, error) {
	if writer == nil || writer.writer == nil {
		return len(payload), nil
	}
	if len(writer.scrubber.values) == 0 {
		_, err := writer.writer.Write(payload)
		return len(payload), err
	}

	value := writer.tail + string(payload)
	keep := writer.scrubber.maxLen - 1
	if keep < 0 {
		keep = 0
	}
	if len(value) <= keep {
		writer.tail = value
		return len(payload), nil
	}

	flushAt := len(value) - keep
	flush := writer.scrubber.Scrub(value[:flushAt])
	writer.tail = value[flushAt:]
	_, err := io.WriteString(writer.writer, flush)
	return len(payload), err
}

func (writer *scrubbingWriter) Flush() error {
	if writer == nil || writer.writer == nil || writer.tail == "" {
		return nil
	}

	tail := writer.scrubber.Scrub(writer.tail)
	writer.tail = ""
	_, err := io.WriteString(writer.writer, tail)
	return err
}
