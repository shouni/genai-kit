package gemini_test

import (
	"errors"
	"fmt"

	"github.com/shouni/genai-kit/gemini"
)

// 生成パラメータはリクエストごとに GenerateOptions で指定します。
// Temperature のようにゼロ値が意味を持つ項目はポインタなので、new(式) で渡します。
func ExampleGenerateOptions() {
	opts := gemini.GenerateOptions{
		SystemPrompt:    "あなたは簡潔に答えるアシスタントです。",
		Temperature:     new(float32(0)), // 0 = 最も決定的
		MaxOutputTokens: 1024,
		StopSequences:   []string{"###"},
	}

	fmt.Println(*opts.Temperature, opts.MaxOutputTokens)
	// Output: 0 1024
}

// 思考機能はコストとレイテンシに直結するため、ThinkingBudget を 0 にして
// 明示的に無効化できます。モデルを跨いで使う場合は ThinkingLevel が移植性に優れます。
func ExampleGenerateOptions_thinking() {
	// 思考を無効化してレイテンシを抑える。
	fast := gemini.GenerateOptions{ThinkingBudget: new(int32(0))}

	// 段階指定。ThinkingBudget と併用した場合はこちらが優先される。
	deep := gemini.GenerateOptions{
		ThinkingLevel:   gemini.ThinkingHigh,
		IncludeThoughts: true,
	}

	fmt.Println(*fast.ThinkingBudget, deep.ThinkingLevel, deep.IncludeThoughts)
	// Output: 0 HIGH true
}

// 構造化出力のスキーマは、genai SDK を import せずにこのパッケージの別名で書けます。
func ExampleSchema() {
	opts := gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema: &gemini.Schema{
			Type: gemini.TypeObject,
			Properties: map[string]*gemini.Schema{
				"title":    {Type: gemini.TypeString},
				"keywords": {Type: gemini.TypeArray, Items: &gemini.Schema{Type: gemini.TypeString}},
			},
			Required: []string{"title"},
		},
	}

	fmt.Println(opts.ResponseSchema.Type, opts.ResponseSchema.Required)
	// Output: OBJECT [title]
}

// 生成失敗の理由は errors.Is / errors.AsType で分類できます。
// ブロックはリトライしても解決しないため、プロンプトの見直しが必要です。
func ExampleAPIResponseError() {
	// 実際には Generate / GenerateText が返すエラーを受け取ります。
	var err error = &gemini.APIResponseError{
		Reason:  gemini.ErrEmptyResponse,
		Message: "Vertex AI から空のレスポンスが返されました",
	}

	switch {
	case errors.Is(err, gemini.ErrBlocked):
		fmt.Println("blocked: プロンプトを見直してください")
	case errors.Is(err, gemini.ErrEmptyResponse):
		fmt.Println("empty: 候補が返りませんでした")
	}

	if apiErr, ok := errors.AsType[*gemini.APIResponseError](err); ok {
		fmt.Println("reason:", apiErr.Reason)
	}

	// Output:
	// empty: 候補が返りませんでした
	// reason: gemini: empty response
}

// CleanJSONResponse は構造化出力に混じる Markdown 装飾や末尾ノイズを取り除きます。
func ExampleCleanJSONResponse() {
	fmt.Println(gemini.CleanJSONResponse("```json\n{\"title\":\"ok\"}\n```"))
	fmt.Println(gemini.CleanJSONResponse(`{"title":"ok"} このあとに説明が続く`))
	fmt.Println(gemini.CleanJSONResponse(`前置き [1,2,3] 後置き`))

	// Output:
	// {"title":"ok"}
	// {"title":"ok"}
	// [1,2,3]
}

// Config は Vertex AI 専用で、ProjectID と LocationID の両方が必須です。
// 認証は ADC に従うため、HTTPClient を差し替えても認証は落ちません。
func ExampleConfig() {
	cfg := gemini.Config{
		ProjectID:  "my-project",
		LocationID: "asia-northeast1",
		MaxRetries: 3,
	}

	fmt.Println(cfg.ProjectID, cfg.LocationID, cfg.MaxRetries)
	// Output: my-project asia-northeast1 3
}
