package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

func appendJobLogLine(logPath string, line string, scrubbers ...secretScrubber) {
	logPath = strings.TrimSpace(logPath)
	line = strings.TrimSpace(line)
	if logPath == "" || line == "" {
		return
	}
	if len(scrubbers) > 0 {
		line = scrubbers[0].Scrub(line)
	}

	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		log.Printf("warning: failed to append job log line to %q: %v", logPath, err)
		return
	}
	defer logFile.Close()

	if _, err := fmt.Fprintln(logFile, line); err != nil {
		log.Printf("warning: failed to write job log line to %q: %v", logPath, err)
	}
}

func appendJobLogError(logPath string, errorMessage string, scrubbers ...secretScrubber) {
	errorMessage = normalizeJobFailureMessage(errorMessage)
	if errorMessage == "" {
		return
	}

	appendJobLogLine(logPath, "error: "+errorMessage, scrubbers...)
}
