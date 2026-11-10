package gemini

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanJSONResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "JSON that parses as is",
			input: `{"title":"test"}`,
			want:  `{"title":"test"}`,
		},
		{
			name:  "wrapped in a Markdown fence",
			input: "```json\n{\"title\":\"test\"}\n```",
			want:  `{"title":"test"}`,
		},
		{
			name:  "explanation before and after",
			input: `Here is the JSON: {"title":"test"} done.`,
			want:  `{"title":"test"}`,
		},
		{
			name:  "nested JSON",
			input: `{"a":{"b":"c"}}`,
			want:  `{"a":{"b":"c"}}`,
		},
		{
			name:  "returns unfixable input as is",
			input: `{"unclosed"`,
			want:  `{"unclosed"`,
		},
		{
			name:  "returns as is when there are no brackets",
			input: `no json here`,
			want:  `no json here`,
		},
		{
			name:  "returns as is when the extracted value is broken",
			input: `prefix {broken json} suffix`,
			want:  `prefix {broken json} suffix`,
		},
		{
			name:  "closed with ')' instead of a closing bracket",
			input: "{\"title\":\"test\",\"narrative\":\"hello\")",
			want:  `{"title":"test","narrative":"hello"}`,
		},
		{
			name:  "wrong closing bracket and trailing whitespace",
			input: "{\"title\":\"test\")\n",
			want:  `{"title":"test"}`,
		},
		{
			// A real pattern from a production incident: an extra '}' and a fragment of the body follow a complete JSON value.
			name:  "extra brackets and body text after complete JSON",
			input: "{\n  \"title\": \"調和の翼\",\n  \"narrative\": \"王道アニソン。\"\n}\n}\nアニソンファンタジー。\"\n})",
			want:  "{\n  \"title\": \"調和の翼\",\n  \"narrative\": \"王道アニソン。\"\n}",
		},
		{
			name:  "only an explanation follows",
			input: `{"title":"test"} これは補足の説明です。`,
			want:  `{"title":"test"}`,
		},
		{
			// Counting brackets inside string literals would extract the wrong span here.
			name:  "brackets inside a string",
			input: `{"lyrics":"[Verse]\n光 {影} 空"} garbage }`,
			want:  `{"lyrics":"[Verse]\n光 {影} 空"}`,
		},
		{
			name:  "top level is an array",
			input: "前置き [1,2,3] 後置き",
			want:  `[1,2,3]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := CleanJSONResponse(tt.input); got != tt.want {
				t.Errorf("CleanJSONResponse() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCleanJSONResponseRepairsBrokenStrings checks that breakage inside string literals is
// repaired. It happens even with structured output specified, and since the breakage occurs
// after the response has been returned in full, an API retry does not fix it. It shows up when
// a multi-line body, such as a script excerpt, lyrics or dialogue, is carried in JSON.
func TestCleanJSONResponseRepairsBrokenStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "raw newline inside a string",
			input: "{\"excerpt\":\"1行目\n2行目\"}",
			want:  `{"excerpt":"1行目\n2行目"}`,
		},
		{
			name:  "raw tab inside a string",
			input: "{\"code\":\"if x:\n\tpass\"}",
			want:  `{"code":"if x:\n\tpass"}`,
		},
		{
			name:  "bare backslash inside a string",
			input: `{"pattern":"\d+件"}`,
			want:  `{"pattern":"\\d+件"}`,
		},
		{
			// Looking at valid escapes one byte at a time would mistake \" for the end of the string.
			name:  "valid escapes survive the backslash repair",
			input: `{"quote":"彼は\"はい\"と言った\d"}`,
			want:  `{"quote":"彼は\"はい\"と言った\\d"}`,
		},
		{
			name:  "raw newline inside a fence",
			input: "```json\n{\"lyrics\":\"A\nB\"}\n```",
			want:  `{"lyrics":"A\nB"}`,
		},
		{
			name:  "raw newline and a trailing explanation",
			input: "{\"a\":\"x\ny\"} 以上です。",
			want:  `{"a":"x\ny"}`,
		},
		{
			name:  "control characters without a short form become \\u escapes",
			input: "{\"a\":\"\x00\"}",
			want:  `{"a":"\u0000"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := CleanJSONResponse(tt.input)
			if got != tt.want {
				t.Errorf("CleanJSONResponse() = %q, want %q", got, tt.want)
			}
			if !json.Valid([]byte(got)) {
				t.Errorf("the repaired result must be valid JSON: %q", got)
			}
		})
	}
}

// TestCleanJSONResponseNeverWorsens checks that unfixable input is not made worse.
// This keeps the caller's error message pointing at the original breakage.
func TestCleanJSONResponseNeverWorsens(t *testing.T) {
	t.Parallel()

	unrepairable := []string{
		`{"a": }`,
		`{"unterminated": "文字列が閉じていません`,
		`ここには JSON がありません`,
		"",
	}

	for _, in := range unrepairable {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			if got := CleanJSONResponse(in); got != in {
				t.Errorf("CleanJSONResponse(%q) = %q, want the input unchanged", in, got)
			}
		})
	}
}

// TestCleanJSONResponseLeavesValidInputAlone checks that input that parses is not changed by
// a single byte. Newlines and indentation used for formatting are preserved too.
func TestCleanJSONResponseLeavesValidInputAlone(t *testing.T) {
	t.Parallel()

	valid := []string{
		`{"a":1}`,
		"{\n  \"a\": 1,\n  \"b\": [2, 3]\n}",
		`[{"a":1},{"b":2}]`,
		`{"path":"C:\\tmp\\a.txt"}`,
		`{"text":"改行は\nこう"}`,
	}

	for _, in := range valid {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			if got := CleanJSONResponse(in); got != in {
				t.Errorf("CleanJSONResponse(%q) = %q, want the input unchanged", in, got)
			}
		})
	}
}

// FuzzCleanJSONResponse checks that repairing LLM output does not panic, and that it "never
// produces invalid JSON as a result of changing the input".
//
// This function always runs before json.Unmarshal, so if it breaks, the whole structured-output
// path breaks. Hand-picked cases cannot cover every place where string-literal scanning and
// bracket extraction combine.
func FuzzCleanJSONResponse(f *testing.F) {
	seeds := []string{
		`{"title":"ok"}`,
		"```json\n{\"title\":\"ok\"}\n```",
		`{"title":"ok"} 余計な説明文`,
		`{"title":"ok"}}`,
		`{"title":"ok"`,
		`{"title":"ok")`,
		`{"title":"波括弧 } を含む文字列"}`,
		`[{"a":1},{"b":2}]`,
		`前置き [1,2,3] 後置き`,
		`[1,2,3]]`,
		`[{"a":1}`,
		`{"nested":{"deep":[1,{"x":"}"}]}}`,
		"",
		"no json here",
		"{",
		"[",
		"}{",
		`{"a":"\"escaped\""}`,
		strings.Repeat("{", 128),
		// Breakage inside string literals (raw control characters, bare backslashes).
		"{\"a\":\"1行目\n2行目\"}",
		"{\"a\":\"tab\there\"}",
		`{"a":"\d+"}`,
		`{"a":"\\d"}`,
		`{"a":"彼は\"はい\"と言った\d"}`,
		"```json\n{\"a\":\"A\nB\"}\n```",
		"{\"a\":\"x\ny\"} 以上です。",
		"{\"a\":\"\x00\"}",
		`{"a":"\u00"}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		got := CleanJSONResponse(raw)

		// If the output equals the input, it means "could not be corrected", so there is no invariant.
		if got == raw {
			return
		}

		// Once the input has been rewritten, the result must always be valid JSON.
		// Otherwise the caller's json.Unmarshal would fail in a worse state than with the original input.
		if !json.Valid([]byte(got)) {
			t.Fatalf("rewrote the input but produced invalid JSON\ninput: %q\noutput: %q", raw, got)
		}
	})
}
