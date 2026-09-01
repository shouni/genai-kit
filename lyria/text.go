package lyria

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// LyricsGenerator は歌詞生成を担う役割です。
type LyricsGenerator interface {
	GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error)
}

// Composer は楽曲の設計（レシピ構築）を担う役割です。
type Composer interface {
	Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error)
}

// TextPromptBuilder は歌詞およびレシピ生成のためのプロンプトを構築するインターフェースです。
//
// メソッドは歌詞やレシピそのものではなくプロンプト文字列を返します。名前が
// Generate で始まると LyricsGenerator.GenerateLyrics（歌詞を生成する側）と紛れる
// ため、「何のプロンプトか」を名詞で示します。このパッケージで Generator は
// AI を呼ぶ側を、Builder は AI を呼ばない組み立て役を指します。
type TextPromptBuilder interface {
	LyricsPrompt(mode string, input string) (string, error)
	RecipePrompt(mode string, lyrics *LyricsDraft) (string, error)
}

// CollectedContent は、歌詞生成と楽曲生成の入力になるテキストと画像です。
type CollectedContent struct {
	Prompt string
	Images []ImagePayload
}

const defaultComposeMode = "default"

// textGenerator は Gemini を使った歌詞生成と楽曲レシピ生成をまとめて扱います。
type textGenerator struct {
	aiClient     gemini.Generator
	prompts      TextPromptBuilder
	defaultModel string
	// guard は nil で「制限なし・既定の上限時間」を意味します。
	guard *callguard.Guard
	group callguard.Group
}

// resolveModel は呼び出しごとのモデル指定があればそれを、なければデフォルトモデルを返します。
func (g *textGenerator) resolveModel(override string) string {
	if override != "" {
		return override
	}
	return g.defaultModel
}

// generateJSON は歌詞・レシピ生成で共通の「singleflight → Gemini 呼び出し → JSON デコード」
// フローを実行します。kind はエラーメッセージと singleflight キーの識別子です。
// 戻り値は singleflight で共有されるため、呼び出し側で複製してから返してください。
//
// Go 1.27 でメソッドが型パラメータを持てるようになったため、レシーバを第 2 引数で
// 受け取る関数ではなくメソッドとして書けます。
func (g *textGenerator) generateJSON[T any](ctx context.Context, kind, model, prompt string, seed *int64, schema *gemini.Schema) (*T, error) {
	// seed は生成結果を変えるため、必ずキーに含める。含め忘れると同一プロンプトで
	// seed 違いの同時呼び出しが 1 回の生成結果を共有してしまう。
	key := callguard.Key(kind, model, prompt, callguard.SeedKey(seed))
	return callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*T, error) {
		resp, err := g.aiClient.Generate(execCtx, model, prompt, nil, jsonOptions(seed, schema))
		if err != nil {
			return nil, fmt.Errorf("%s generation failed (model: %s): %w", kind, model, err)
		}
		if resp == nil {
			return nil, fmt.Errorf("%w: %s response is nil", ErrInvalidResponse, kind)
		}

		raw := strings.TrimSpace(resp.Text)
		if raw == "" {
			return nil, fmt.Errorf("%w: AI returned an empty string for the %s", ErrInvalidResponse, kind)
		}

		jsonStr := gemini.CleanJSONResponse(raw)
		var out T
		if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
			// 生出力の全文はログを肥大化させるため、診断に足りる先頭だけを残す。
			return nil, fmt.Errorf("%w: failed to unmarshal %s json: %w (raw: %s)",
				ErrInvalidResponse, kind, err, truncateForError(jsonStr))
		}
		return &out, nil
	})
}

// GenerateLyrics は収集済みコンテンツから歌詞ドラフトを生成します。
func (g *textGenerator) GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: collected content", ErrNilInput)
	}

	promptText, err := g.prompts.LyricsPrompt(ai.LyricsMode, input.Prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to build lyrics prompt: %w", err)
	}

	lyrics, err := g.generateJSON[LyricsDraft](ctx, "lyrics", g.resolveModel(ai.TextModel), promptText, ai.Seed, lyricsDraftSchema())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(lyrics.Lyrics) == "" {
		return nil, ErrEmptyLyrics
	}

	return lyrics.Clone(), nil
}

// Compose は歌詞ドラフトから楽曲レシピを生成します。
func (g *textGenerator) Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error) {
	if lyrics == nil {
		return nil, fmt.Errorf("%w: lyrics draft", ErrNilInput)
	}

	targetMode := ai.ComposeMode
	if targetMode == "" {
		targetMode = defaultComposeMode
	}

	promptText, err := g.prompts.RecipePrompt(targetMode, lyrics)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt (mode: %s): %w", targetMode, err)
	}

	shared, err := g.generateJSON[MusicRecipe](ctx, "compose", g.resolveModel(ai.TextModel), promptText, ai.Seed, musicRecipeSchema())
	if err != nil {
		return nil, err
	}

	// 呼び出し元固有の情報は共有結果を複製してから付与する。
	recipe := shared.Clone()
	recipe.Lyrics = lyrics.Clone()
	recipe.AIModels = ai
	if ai.Seed != nil {
		seed := *ai.Seed
		recipe.Seed = &seed
	}
	return recipe, nil
}
