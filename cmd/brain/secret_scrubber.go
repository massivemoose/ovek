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
		return writer.writer.Write(payload)
	}

	writer.tail += string(payload)
	return len(payload), writer.flush(false)
}

func (writer *scrubbingWriter) Flush() error {
	return writer.flush(true)
}

func (writer *scrubbingWriter) flush(final bool) error {
	if writer == nil || writer.writer == nil || writer.tail == "" {
		return nil
	}

	var output strings.Builder
	for len(writer.tail) > 0 {
		if !final && len(writer.tail) < writer.scrubber.maxLen {
			break
		}

		matched := ""
		// ponytail: project secret sets are small; use a multi-pattern matcher only if profiling proves necessary.
		for _, secret := range writer.scrubber.values {
			if strings.HasPrefix(writer.tail, secret) {
				matched = secret
				break
			}
		}
		if matched != "" {
			output.WriteString("[redacted]")
			writer.tail = writer.tail[len(matched):]
			continue
		}

		output.WriteByte(writer.tail[0])
		writer.tail = writer.tail[1:]
	}

	if output.Len() == 0 {
		return nil
	}
	written, err := io.WriteString(writer.writer, output.String())
	if err == nil && written != output.Len() {
		err = io.ErrShortWrite
	}
	return err
}
