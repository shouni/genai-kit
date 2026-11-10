package gemini

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CleanJSONResponse repairs JSON returned by the AI when it cannot be parsed as is.
//
// Even with structured output specified via GenerateOptions.ResponseSchema / ResponseJSONSchema,
// the model sometimes breaks it in the following ways. All of them happen after the response has
// been returned in full, so an API retry does not fix them.
//
//   - wrapping it in a Markdown fence (```json … ```)
//   - appending an explanation or extra closing brackets after a complete JSON value
//   - closing with ')' or the like instead of '}'
//   - forgetting to escape a backslash inside a string (when quoting source code or paths)
//   - putting raw newlines or tabs inside a string (when quoting a multi-line body)
//
// The last two happen especially when a multi-line body, such as a script excerpt, lyrics or
// dialogue, is carried in JSON. Unless they are fixed here, a generation that took minutes is lost
// entirely, leaving only a one-line "cannot be parsed".
//
// Input that already parses is not changed by a single byte. If the repair does not produce valid
// JSON, the input is returned as is. Not making things worse takes priority, so that the caller's
// error message keeps pointing at the original breakage.
//
// go-review-kit's review.SanitizeJSON solves the same problem. That module keeps its go.mod down to
// go-git alone, so it is not merged into this module and deliberately stays a separate implementation
// (rescuing a truncated response drops data and needs a way to notify the caller, so only that one
// has it).
func CleanJSONResponse(input string) string {
	if json.Valid([]byte(input)) {
		return input
	}

	if cleaned, ok := extractJSONValue(input); ok {
		return cleaned
	}

	// Broken escapes inside strings are not fixed by matching brackets.
	// After filling in backslashes and raw control characters, extracting the value is tried again.
	escaped := escapeStringLiterals(input)
	if escaped != input {
		if json.Valid([]byte(escaped)) {
			return escaped
		}
		if cleaned, ok := extractJSONValue(escaped); ok {
			return cleaned
		}
	}

	return input
}

// extractJSONValue extracts the first complete JSON value that appears.
// ok is false if none can be extracted.
func extractJSONValue(input string) (string, bool) {
	start, closer := firstJSONStart(input)
	if start == -1 {
		return "", false
	}

	// json.Decoder correctly skips brackets inside string literals and stops where the value
	// closes. The point is that fences or explanations around it can be dropped without counting brackets.
	var value json.RawMessage
	if err := json.NewDecoder(strings.NewReader(input[start:])).Decode(&value); err == nil {
		return string(value), true
	}

	// Correct the case where the LLM closes with ')' or the like instead of '}'.
	trimmed := strings.TrimRight(input[start:], " \t\n\r)]},;")
	if repaired := trimmed + closer; json.Valid([]byte(repaired)) {
		return repaired, true
	}

	return "", false
}

// firstJSONStart returns the start position of the first JSON value and the matching closing bracket.
// To also support schemas whose top level is an array (e.g. a list of chapters),
// whichever of '{' and '[' comes first is used.
func firstJSONStart(input string) (start int, closer string) {
	obj := strings.Index(input, "{")
	arr := strings.Index(input, "[")

	switch {
	case obj == -1 && arr == -1:
		return -1, ""
	case arr == -1 || (obj != -1 && obj < arr):
		return obj, "}"
	default:
		return arr, "]"
	}
}

// escapeStringLiterals repairs broken notation inside string literals in a single scan.
//
// It fixes two kinds, both of which appear when "the model poured a body of text straight into JSON".
//
//   - Bare backslashes: in JSON, only "\/bfnrtu may follow a backslash, so a `\X` followed by
//     anything else is unambiguously a literal backslash that was not escaped (it appears when
//     quoting a regular expression's `\d` or a path). `\X` → `\\X` loses no information and
//     never makes the interpretation ambiguous.
//   - Raw control characters: JSON does not allow characters below U+0020 as is inside a string.
//     They are characters that must not appear at that position, so mapping them to an escape does not change the meaning.
//
// The point is fixing both kinds in a single scan. Running two separate scans would make the order
// matter: if the control-character repair (which adds a backslash, as in `\n`) ran first, the later
// scan looking for bare backslashes would double the escape that had just been added.
//
// Nothing outside strings is touched. In JSON syntax, backslashes only appear inside strings, and
// newlines and indentation outside are legitimate formatting.
func escapeStringLiterals(input string) string {
	var b strings.Builder
	b.Grow(len(input))

	inString := false
	for i := 0; i < len(input); i++ {
		c := input[i]

		if !inString {
			if c == '"' {
				inString = true
			}
			b.WriteByte(c)
			continue
		}

		switch {
		case c == '"':
			inString = false
			b.WriteByte(c)
		case c == '\\':
			if i+1 < len(input) && isJSONEscape(input[i+1]) {
				// Valid escapes pass through as is. Advancing two bytes here is the point: looking at
				// `\\` one byte at a time would mistake the second for a bare backslash, and with
				// `\"` the quote would be mistaken for the end of the string.
				b.WriteByte(c)
				b.WriteByte(input[i+1])
				i++
				continue
			}
			// What follows is an invalid escape (or the end), so double it as a literal.
			b.WriteString(`\\`)
		case c < 0x20:
			b.WriteString(controlEscape(c))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// controlEscape returns the JSON escape notation for a control character.
// Short forms are used where they exist. People sometimes read logs and outputs, and `\n` is
// more readable than newlines lined up as six-character \u escapes.
func controlEscape(c byte) string {
	switch c {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	default:
		return fmt.Sprintf(`\u%04x`, c)
	}
}

// isJSONEscape reports whether the character can follow a backslash.
//
// \u requires the following four characters to be hexadecimal, but that is not checked. There is
// no unambiguous way to fix a broken \u, and touching it would only break it differently.
func isJSONEscape(c byte) bool {
	switch c {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
		return true
	default:
		return false
	}
}
