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
			name:  "そのまま解釈できる JSON",
			input: `{"title":"test"}`,
			want:  `{"title":"test"}`,
		},
		{
			name:  "Markdown のフェンスで包まれている",
			input: "```json\n{\"title\":\"test\"}\n```",
			want:  `{"title":"test"}`,
		},
		{
			name:  "前後に説明文が付いている",
			input: `Here is the JSON: {"title":"test"} done.`,
			want:  `{"title":"test"}`,
		},
		{
			name:  "入れ子の JSON",
			input: `{"a":{"b":"c"}}`,
			want:  `{"a":{"b":"c"}}`,
		},
		{
			name:  "直せない入力はそのまま返す",
			input: `{"unclosed"`,
			want:  `{"unclosed"`,
		},
		{
			name:  "括弧が無ければそのまま返す",
			input: `no json here`,
			want:  `no json here`,
		},
		{
			name:  "取り出した先が壊れていればそのまま返す",
			input: `prefix {broken json} suffix`,
			want:  `prefix {broken json} suffix`,
		},
		{
			name:  "閉じ括弧の代わりに ')' で閉じている",
			input: "{\"title\":\"test\",\"narrative\":\"hello\")",
			want:  `{"title":"test","narrative":"hello"}`,
		},
		{
			name:  "閉じ括弧の誤りと末尾の空白",
			input: "{\"title\":\"test\")\n",
			want:  `{"title":"test"}`,
		},
		{
			// 本番障害の実パターン: 完結した JSON の後に余分な '}' と本文の断片が続く。
			name:  "完結した JSON の後に余分な括弧と本文",
			input: "{\n  \"title\": \"調和の翼\",\n  \"narrative\": \"王道アニソン。\"\n}\n}\nアニソンファンタジー。\"\n})",
			want:  "{\n  \"title\": \"調和の翼\",\n  \"narrative\": \"王道アニソン。\"\n}",
		},
		{
			name:  "末尾に説明文だけが続く",
			input: `{"title":"test"} これは補足の説明です。`,
			want:  `{"title":"test"}`,
		},
		{
			// 文字列リテラルの中の括弧を数えてしまうと、ここで切り出しを誤ります。
			name:  "文字列の中に括弧がある",
			input: `{"lyrics":"[Verse]\n光 {影} 空"} garbage }`,
			want:  `{"lyrics":"[Verse]\n光 {影} 空"}`,
		},
		{
			name:  "トップレベルが配列",
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

// TestCleanJSONResponseRepairsBrokenStrings は、文字列リテラルの中の崩れを補修する
// ことを検証します。構造化出力を指定していても起こり、応答を返しきったあとの崩れ
// なので API の再試行では直りません。台本の抜粋・歌詞・台詞のように複数行の本文を
// JSON に載せる用途で出ます。
func TestCleanJSONResponseRepairsBrokenStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "文字列の中の生の改行",
			input: "{\"excerpt\":\"1行目\n2行目\"}",
			want:  `{"excerpt":"1行目\n2行目"}`,
		},
		{
			name:  "文字列の中の生のタブ",
			input: "{\"code\":\"if x:\n\tpass\"}",
			want:  `{"code":"if x:\n\tpass"}`,
		},
		{
			name:  "文字列の中の裸のバックスラッシュ",
			input: `{"pattern":"\d+件"}`,
			want:  `{"pattern":"\\d+件"}`,
		},
		{
			// 正しいエスケープを 1 バイトずつ見ると、\" を文字列の終わりと取り違えます。
			name:  "正しいエスケープはバックスラッシュ補修を生き延びる",
			input: `{"quote":"彼は\"はい\"と言った\d"}`,
			want:  `{"quote":"彼は\"はい\"と言った\\d"}`,
		},
		{
			name:  "フェンスの中に生の改行",
			input: "```json\n{\"lyrics\":\"A\nB\"}\n```",
			want:  `{"lyrics":"A\nB"}`,
		},
		{
			name:  "生の改行と末尾の説明文",
			input: "{\"a\":\"x\ny\"} 以上です。",
			want:  `{"a":"x\ny"}`,
		},
		{
			name:  "短い表記の無い制御文字は \\u エスケープになる",
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
				t.Errorf("補修後は妥当な JSON である必要があります: %q", got)
			}
		})
	}
}

// TestCleanJSONResponseNeverWorsens は、直せない入力を悪化させないことを検証します。
// 呼び出し側のエラーメッセージが元の壊れ方を指したままになるようにするためです。
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
				t.Errorf("CleanJSONResponse(%q) = %q, want 入力のまま", in, got)
			}
		})
	}
}

// TestCleanJSONResponseLeavesValidInputAlone は、解釈できる入力を 1 バイトも
// 変えないことを検証します。整形に使われた改行やインデントも保ちます。
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
				t.Errorf("CleanJSONResponse(%q) = %q, want 入力のまま", in, got)
			}
		})
	}
}

// FuzzCleanJSONResponse は、LLM 出力の補修が panic せず、かつ「入力を変えた結果として
// 不正な JSON を作り出さない」ことを検証します。
//
// この関数は json.Unmarshal の前段で必ず通るため、ここが壊れると構造化出力の経路
// 全体が壊れます。手で並べたケースでは、文字列リテラルの走査と括弧の切り出しが
// 組み合わさる場所を網羅しきれません。
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
		// 文字列リテラルの中の崩れ（生の制御文字・裸のバックスラッシュ）。
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

		// 出力が入力と同じなら「補正できなかった」ことを意味するので不変条件はない。
		if got == raw {
			return
		}

		// 入力を書き換えた以上、その結果は必ず妥当な JSON でなければならない。
		// そうでないと呼び出し側の json.Unmarshal が、元の入力より悪い状態で失敗する。
		if !json.Valid([]byte(got)) {
			t.Fatalf("入力を書き換えたのに不正な JSON になりました\n入力: %q\n出力: %q", raw, got)
		}
	})
}
