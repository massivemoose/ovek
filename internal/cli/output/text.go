package output

import (
	"fmt"
	"io"
	"os"
	"strconv"
)

func WriteSection(w io.Writer, title string) {
	_, _ = fmt.Fprintf(w, "%s\n", title)
}

func WriteSuccess(w io.Writer, message string) {
	_, _ = fmt.Fprintf(w, "%s\n", message)
}

func WriteEmpty(w io.Writer, message string) {
	_, _ = fmt.Fprintf(w, "%s\n", message)
}

func WriteNote(w io.Writer, message string) {
	_, _ = fmt.Fprintf(w, "Note: %s\n", message)
}

func WriteWarning(w io.Writer, message string) {
	_, _ = fmt.Fprintf(w, "Warning: %s\n", message)
}

func WriteNextStep(w io.Writer, pairs [][2]string) {
	WriteSection(w, "Next Step")
	WriteKeyValues(w, pairs)
}

func WriteCommandSuggestion(w io.Writer, label string, command string) {
	WriteKeyValues(w, [][2]string{{label, command}})
}

func WriteKeyValues(w io.Writer, pairs [][2]string) {
	maxKeyLength := 0
	for _, pair := range pairs {
		if len(pair[0]) > maxKeyLength {
			maxKeyLength = len(pair[0])
		}
	}

	for _, pair := range pairs {
		_, _ = fmt.Fprintf(w, "%-*s  %s\n", maxKeyLength, pair[0], pair[1])
	}
}

func WriteTable(w io.Writer, headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}

	widths := make([]int, len(headers))
	fillTableWidths(widths, headers, rows)

	writeRow(w, headers, widths)
	for _, row := range rows {
		writeRow(w, row, widths)
	}
}

type TableOptions struct {
	MaxWidth    int
	ForceNarrow bool
}

func WriteAdaptiveTable(w io.Writer, headers []string, rows [][]string, options TableOptions) {
	if len(headers) == 0 {
		return
	}
	if options.ForceNarrow {
		writeRecords(w, headers, rows)
		return
	}

	widths := make([]int, len(headers))
	fillTableWidths(widths, headers, rows)
	if options.MaxWidth > 0 && tableWidth(widths) > options.MaxWidth {
		writeRecords(w, headers, rows)
		return
	}

	writeRow(w, headers, widths)
	for _, row := range rows {
		writeRow(w, row, widths)
	}
}

func DetectTerminalWidth(defaultWidth int) int {
	for _, name := range []string{"OVEK_COLUMNS", "COLUMNS"} {
		value, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		width, err := strconv.Atoi(value)
		if err == nil && width > 0 {
			return width
		}
	}
	return defaultWidth
}

func fillTableWidths(widths []int, headers []string, rows [][]string) {
	for index, header := range headers {
		widths[index] = len(header)
	}
	for _, row := range rows {
		for index, value := range row {
			if index < len(widths) && len(value) > widths[index] {
				widths[index] = len(value)
			}
		}
	}
}

func tableWidth(widths []int) int {
	total := 0
	for index, width := range widths {
		if index > 0 {
			total += 2
		}
		total += width
	}
	return total
}

func writeRecords(w io.Writer, headers []string, rows [][]string) {
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			_, _ = fmt.Fprintln(w)
		}
		for index, header := range headers {
			value := "-"
			if index < len(row) && row[index] != "" {
				value = row[index]
			}
			_, _ = fmt.Fprintf(w, "%s: %s\n", header, value)
		}
	}
}

func writeRow(w io.Writer, row []string, widths []int) {
	for index, value := range row {
		if index >= len(widths) {
			break
		}
		if index > 0 {
			_, _ = fmt.Fprint(w, "  ")
		}
		_, _ = fmt.Fprintf(w, "%-*s", widths[index], value)
	}
	_, _ = fmt.Fprintln(w)
}
