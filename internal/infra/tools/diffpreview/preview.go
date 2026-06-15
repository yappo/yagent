package diffpreview

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxLineRunes       = 160
	maxChangeLines     = 32
	maxCombinedLines   = 48
	truncatedLineLabel = "... preview truncated"
)

type Stats struct {
	Files     int
	Additions int
	Deletions int
}

func TextChange(path string, before []byte, beforeExists bool, after string) string {
	beforeText := string(before)
	beforeLines := splitLines(beforeText)
	afterLines := splitLines(after)

	lines := []string{path}
	if beforeExists {
		lines = append(lines, fmt.Sprintf("- before: %d lines, %d bytes", len(beforeLines), len(before)))
	} else {
		lines = append(lines, "- before: new file")
	}
	lines = append(lines, fmt.Sprintf("+ after: %d lines, %d bytes", len(afterLines), len([]byte(after))))

	if beforeExists && beforeText == after {
		lines = append(lines, "no content changes")
		return strings.Join(lines, "\n")
	}

	prefix, oldPart, newPart := changedRegion(beforeLines, afterLines)
	lines = append(lines, fmt.Sprintf("@@ changed near line %d @@", prefix+1))
	return strings.Join(appendChangeLines(lines, oldPart, newPart, maxChangeLines), "\n")
}

func TextChangeStats(before []byte, beforeExists bool, after string) Stats {
	beforeText := string(before)
	beforeLines := splitLines(beforeText)
	afterLines := splitLines(after)
	if beforeExists && beforeText == after {
		return Stats{Files: 1}
	}
	_, oldPart, newPart := changedRegion(beforeLines, afterLines)
	return Stats{
		Files:     1,
		Additions: len(newPart),
		Deletions: len(oldPart),
	}
}

func Replacement(path string, oldText string, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	lines := []string{
		path,
		fmt.Sprintf("- old_text: %d lines, %d bytes", len(oldLines), len([]byte(oldText))),
		fmt.Sprintf("+ new_text: %d lines, %d bytes", len(newLines), len([]byte(newText))),
		"@@ replacement @@",
	}
	return strings.Join(appendChangeLines(lines, oldLines, newLines, maxChangeLines), "\n")
}

func ReplacementStats(oldText string, newText string) Stats {
	return Stats{
		Files:     1,
		Additions: len(splitLines(newText)),
		Deletions: len(splitLines(oldText)),
	}
}

func MergeStats(items []Stats) Stats {
	var merged Stats
	for _, item := range items {
		merged.Files += item.Files
		merged.Additions += item.Additions
		merged.Deletions += item.Deletions
	}
	return merged
}

func Combine(previews []string) string {
	lines := make([]string, 0, maxCombinedLines)
	for i, preview := range previews {
		preview = strings.TrimSpace(preview)
		if preview == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "---")
		}
		for _, line := range strings.Split(preview, "\n") {
			if len(lines) >= maxCombinedLines {
				lines = append(lines, truncatedLineLabel)
				return strings.Join(lines, "\n")
			}
			lines = append(lines, line)
		}
		if i == len(previews)-1 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func changedRegion(beforeLines []string, afterLines []string) (int, []string, []string) {
	prefix := 0
	for prefix < len(beforeLines) && prefix < len(afterLines) && beforeLines[prefix] == afterLines[prefix] {
		prefix++
	}

	beforeEnd := len(beforeLines)
	afterEnd := len(afterLines)
	for beforeEnd > prefix && afterEnd > prefix && beforeLines[beforeEnd-1] == afterLines[afterEnd-1] {
		beforeEnd--
		afterEnd--
	}

	return prefix, beforeLines[prefix:beforeEnd], afterLines[prefix:afterEnd]
}

func appendChangeLines(dst []string, oldLines []string, newLines []string, limit int) []string {
	remaining := limit
	oldTruncated := false
	newTruncated := false
	dst, oldTruncated = appendPrefixedLines(dst, "-", oldLines, &remaining)
	dst, newTruncated = appendPrefixedLines(dst, "+", newLines, &remaining)
	if oldTruncated || newTruncated {
		dst = append(dst, truncatedLineLabel)
	}
	return dst
}

func appendPrefixedLines(dst []string, prefix string, lines []string, remaining *int) ([]string, bool) {
	for index, line := range lines {
		if *remaining <= 0 {
			return dst, index < len(lines)
		}
		dst = append(dst, prefix+" "+truncateLine(line))
		*remaining--
	}
	return dst, false
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func truncateLine(line string) string {
	line = strings.ReplaceAll(line, "\t", "    ")
	if utf8.RuneCountInString(line) <= maxLineRunes {
		return line
	}
	runes := []rune(line)
	return string(runes[:maxLineRunes]) + "..."
}
