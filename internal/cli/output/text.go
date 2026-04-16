package output

import (
	"fmt"
	"io"
)

func WriteSection(w io.Writer, title string) {
	_, _ = fmt.Fprintf(w, "%s\n", title)
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

	writeRow(w, headers, widths)
	for _, row := range rows {
		writeRow(w, row, widths)
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
