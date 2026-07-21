package app

import (
	"fmt"
	"strings"
)

func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, value := range row {
			if i < len(widths) && len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	printTableRow(headers, widths)
	separators := make([]string, len(headers))
	for i, width := range widths {
		separators[i] = strings.Repeat("-", width)
	}
	printTableRow(separators, widths)
	for _, row := range rows {
		printTableRow(row, widths)
	}
}

func printTableRow(values []string, widths []int) {
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%-*s", width, value)
	}
	fmt.Println()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("-_./:=@", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func windowsDoubleQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func powershellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
