package gemini

import (
	"errors"
	"strings"
	"testing"
)

type decodeTarget struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want decodeTarget
	}{
		{"plain", `{"title":"a","lines":["x"]}`, decodeTarget{Title: "a", Lines: []string{"x"}}},
		{"fenced", "```json\n{\"title\":\"a\",\"lines\":[]}\n```", decodeTarget{Title: "a", Lines: []string{}}},
		{"with preamble", "Here you go:\n{\"title\":\"a\"}\nHope it helps.", decodeTarget{Title: "a"}},
		{"broken escape in string", `{"title":"a\qb"}`, decodeTarget{Title: `a\qb`}},
		// v1 のデコードはメンバー名の大文字小文字を区別しない。v2 だとゼロ値になる。
		{"case-insensitive member", `{"Title":"a"}`, decodeTarget{Title: "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := DecodeJSON[decodeTarget](tt.text)
			if err != nil {
				t.Fatalf("DecodeJSON() error = %v", err)
			}
			if got.Title != tt.want.Title || strings.Join(got.Lines, ",") != strings.Join(tt.want.Lines, ",") {
				t.Errorf("DecodeJSON() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDecodeJSONClassifiesFailures(t *testing.T) {
	t.Parallel()

	t.Run("empty is ErrEmptyResponse", func(t *testing.T) {
		t.Parallel()
		for _, text := range []string{"", "  \n\t"} {
			if _, err := DecodeJSON[decodeTarget](text); !errors.Is(err, ErrEmptyResponse) {
				t.Errorf("DecodeJSON(%q) error = %v, want ErrEmptyResponse", text, err)
			}
		}
	})

	t.Run("unparseable is ErrInvalidJSON with an excerpt", func(t *testing.T) {
		t.Parallel()
		long := "not json at all " + strings.Repeat("x", 500)
		_, err := DecodeJSON[decodeTarget](long)
		if !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("DecodeJSON() error = %v, want ErrInvalidJSON", err)
		}
		if !strings.Contains(err.Error(), "not json at all") || !strings.Contains(err.Error(), "…(truncated)") {
			t.Errorf("error lacks a truncated excerpt: %v", err)
		}
		if len(err.Error()) > 400 {
			t.Errorf("error carries too much of the response (%d bytes)", len(err.Error()))
		}
	})

	t.Run("type mismatch is ErrInvalidJSON", func(t *testing.T) {
		t.Parallel()
		if _, err := DecodeJSON[decodeTarget](`{"title": 42}`); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("DecodeJSON() error = %v, want ErrInvalidJSON", err)
		}
	})
}
