package tui

import (
	"strings"
	"unicode"
)

func normalizePromptReferences(input string, selectedRefs map[string]string) string {
	if input == "" || len(selectedRefs) == 0 {
		return input
	}

	runes := []rune(input)
	var out strings.Builder

	for i := 0; i < len(runes); {
		if runes[i] != '@' {
			out.WriteRune(runes[i])
			i++
			continue
		}

		start := i
		i++
		for i < len(runes) && !unicode.IsSpace(runes[i]) {
			i++
		}

		token := string(runes[start:i])
		if normalized, ok := selectedRefs[token]; ok {
			out.WriteString(normalized)
			continue
		}
		out.WriteString(token)
	}

	return out.String()
}

func (m *model) reconcileSelectedRefs() {
	if len(m.selectedRefs) == 0 {
		return
	}

	tokens := tokenizeInput(m.textarea.Value())
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		seen[token] = struct{}{}
	}

	for token := range m.selectedRefs {
		if _, ok := seen[token]; !ok {
			delete(m.selectedRefs, token)
		}
	}
}

func tokenizeInput(input string) []string {
	var tokens []string
	var current []rune

	flush := func() {
		if len(current) == 0 {
			return
		}
		tokens = append(tokens, string(current))
		current = current[:0]
	}

	for _, r := range input {
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		current = append(current, r)
	}
	flush()
	return tokens
}
