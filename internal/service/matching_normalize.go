package service

import (
	"strings"
	"unicode"
)

var hopMatchStopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {}, "for": {}, "from": {},
	"has": {}, "have": {}, "help": {}, "i": {}, "in": {}, "is": {}, "it": {}, "my": {}, "need": {}, "of": {},
	"on": {}, "or": {}, "please": {}, "someone": {}, "that": {}, "the": {}, "this": {}, "to": {}, "we": {}, "with": {},
}

func normalizeMatchText(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(input))
	lastWasSpace := true
	for _, r := range input {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastWasSpace = false
			continue
		}
		if !lastWasSpace {
			b.WriteByte(' ')
			lastWasSpace = true
		}
	}

	return strings.TrimSpace(b.String())
}

func normalizeAliasText(input string) string {
	return normalizeMatchText(input)
}

func tokenizeMatchText(input string) []string {
	normalized := normalizeMatchText(input)
	if normalized == "" {
		return nil
	}

	raw := strings.Fields(normalized)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		if tok == "" {
			continue
		}
		if _, isStopWord := hopMatchStopWords[tok]; isStopWord {
			continue
		}
		stemmed := stemMatchToken(tok)
		if stemmed == "" {
			continue
		}
		out = append(out, stemmed)
	}
	return out
}

func stemMatchToken(token string) string {
	if len(token) < 4 {
		return token
	}

	if strings.HasSuffix(token, "ies") && len(token) > 4 {
		return token[:len(token)-3] + "y"
	}
	if strings.HasSuffix(token, "ing") && len(token) > 5 {
		base := token[:len(token)-3]
		base = trimRepeatedEndingConsonant(base)
		if strings.HasSuffix(base, "v") {
			return base + "e"
		}
		return base
	}
	if strings.HasSuffix(token, "ed") && len(token) > 4 {
		base := token[:len(token)-2]
		base = trimRepeatedEndingConsonant(base)
		if strings.HasSuffix(base, "v") {
			return base + "e"
		}
		return base
	}
	if strings.HasSuffix(token, "es") && len(token) > 4 {
		base := token[:len(token)-2]
		switch {
		case strings.HasSuffix(token, "ches"),
			strings.HasSuffix(token, "shes"),
			strings.HasSuffix(token, "sses"),
			strings.HasSuffix(token, "xes"),
			strings.HasSuffix(token, "zes"):
			return base
		}
	}
	if strings.HasSuffix(token, "s") && len(token) > 3 && !strings.HasSuffix(token, "ss") {
		return token[:len(token)-1]
	}
	return token
}

func trimRepeatedEndingConsonant(token string) string {
	if len(token) < 2 {
		return token
	}
	last := token[len(token)-1]
	prev := token[len(token)-2]
	if last != prev {
		return token
	}
	if strings.ContainsRune("aeiou", rune(last)) {
		return token
	}
	return token[:len(token)-1]
}
