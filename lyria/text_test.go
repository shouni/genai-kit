package lyria

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTextGenerator は、発射間隔なしの textGenerator を組み立てます。
// guard を nil のままにできるのは、callguard が nil を「制限なし・既定の上限時間」と
// して扱うためです。
func newTextGenerator(ai gemini.Generator, prompts TextPromptBuilder) *textGenerator {
	return &textGenerator{aiClient: ai, prompts: prompts, defaultModel: "gemini-flash"}
}

// TestGenerateLyricsSendsJSONOptions は、歌詞生成が構造化出力の設定つきで送られる
// ことを検証します。スキーマが落ちるとモデル出力が文法的に制約されなくなり、
// JSON の崩れが CleanJSONResponse 頼みになります。
func TestGenerateLyricsSendsJSONOptions(t *testing.T) {
	seed := int64(42)
	ai := textResponder(validLyricsJSON)
	prompts := &stubTextPrompts{lyricsPrompt: "lyrics prompt"}
	g := newTextGenerator(ai, prompts)

	_, err := g.GenerateLyrics(context.Background(), AIModels{LyricsMode: "ballad", Seed: &seed},
		&CollectedContent{Prompt: "収集したテキスト"})
	require.NoError(t, err)

	// プロンプト構築にはモードと入力がそのまま渡る。
	assert.Equal(t, "ballad", prompts.gotLyricsMode)
	assert.Equal(t, "収集したテキスト", prompts.gotLyricsInput)

	call := ai.lastCall(t)
	assert.Equal(t, "lyrics prompt", call.Prompt)
	assert.Equal(t, "application/json", call.Opts.ResponseMIMEType)
	assert.NotNil(t, call.Opts.ResponseSchema, "構造化出力スキーマが指定されていません")
	assert.NotEmpty(t, call.Opts.SafetySettings, "安全設定が指定されていません")
	if assert.NotNil(t, call.Opts.Seed) {
		assert.Equal(t, seed, *call.Opts.Seed)
	}
	assert.Nil(t, call.Attachments, "歌詞生成に添付は送らない")
}

// TestGenerateLyricsResolvesModel は、呼び出しごとのモデル指定が既定より優先される
// ことを検証します。
func TestGenerateLyricsResolvesModel(t *testing.T) {
	tests := []struct {
		name      string
		textModel string
		want      string
	}{
		{"未指定なら既定モデル", "", "gemini-flash"},
		{"指定があればそちらを使う", "gemini-pro", "gemini-pro"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := textResponder(validLyricsJSON)
			g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "p"})

			_, err := g.GenerateLyrics(context.Background(), AIModels{TextModel: tt.textModel},
				&CollectedContent{Prompt: "x"})

			require.NoError(t, err)
			assert.Equal(t, tt.want, ai.lastCall(t).Model)
		})
	}
}

// TestGenerateLyricsCleansJSONResponse は、Markdown のフェンスで包まれた応答でも
// 解釈できることを検証します。構造化出力を指定していても起きる崩れです。
func TestGenerateLyricsCleansJSONResponse(t *testing.T) {
	ai := textResponder("```json\n" + validLyricsJSON + "\n```")
	g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "p"})

	draft, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

	require.NoError(t, err)
	assert.Equal(t, "t", draft.Title)
	assert.Equal(t, "l", draft.Lyrics)
}

func TestGenerateLyricsErrorBranches(t *testing.T) {
	tests := []struct {
		name        string
		prompts     *stubTextPrompts
		ai          *fakeGenerator
		wantErr     error
		wantMessage string
	}{
		{
			name:        "プロンプト構築の失敗",
			prompts:     &stubTextPrompts{lyricsErr: errors.New("prompt boom")},
			ai:          &fakeGenerator{},
			wantMessage: "failed to build lyrics prompt",
		},
		{
			name:        "AI がエラーを返す",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          &fakeGenerator{err: errors.New("ai boom")},
			wantMessage: "lyrics generation failed",
		},
		{
			name:        "AI が nil レスポンスを返す",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          &fakeGenerator{},
			wantErr:     ErrInvalidResponse,
			wantMessage: "lyrics response is nil",
		},
		{
			name:        "AI が空文字列を返す",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          textResponder("   "),
			wantErr:     ErrInvalidResponse,
			wantMessage: "AI returned an empty string",
		},
		{
			name:        "JSON として解釈できない",
			prompts:     &stubTextPrompts{lyricsPrompt: "p"},
			ai:          textResponder("これは JSON ではありません"),
			wantErr:     ErrInvalidResponse,
			wantMessage: "failed to unmarshal lyrics json",
		},
		{
			// スキーマは満たすが本文が空。再試行しても解決しない可能性が高い失敗です。
			name:    "スキーマは通るが歌詞が空",
			prompts: &stubTextPrompts{lyricsPrompt: "p"},
			ai:      textResponder(`{"title":"t","theme":"th","hook":"h","lyrics":"  "}`),
			wantErr: ErrEmptyLyrics,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTextGenerator(tt.ai, tt.prompts)

			_, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantMessage != "" {
				assert.Contains(t, err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestGenerateLyricsRejectsNilInput(t *testing.T) {
	g := newTextGenerator(&fakeGenerator{}, &stubTextPrompts{})

	_, err := g.GenerateLyrics(context.Background(), AIModels{}, nil)

	assert.ErrorIs(t, err, ErrNilInput)
}

// TestComposeAttachesModelsAndSeed は、生成されたレシピに呼び出し時の AIModels と
// Seed が付与されることを検証します。
//
// レシピのスキーマはこれらを意図的に含めておらず（モデルに生成させない）、生成後に
// コードが付けています。ここが抜けると、Seed を指定した再現生成がエラーも警告も
// 無しに効かなくなります。
func TestComposeAttachesModelsAndSeed(t *testing.T) {
	seed := int64(42)
	ai := textResponder(`{"title":"Rainy Amsterdam","tempo":85,"mood":"melancholic"}`)
	prompts := &stubTextPrompts{recipePrompt: "recipe prompt"}
	g := newTextGenerator(ai, prompts)

	lyrics := &LyricsDraft{Title: "Rainy Amsterdam", Lyrics: "Canals reflect the neon lights..."}
	models := AIModels{
		TextModel:   "custom-text-model",
		AudioModel:  "lyria-custom-v1",
		ComposeMode: "jazz",
		Seed:        &seed,
	}

	recipe, err := g.Compose(context.Background(), models, lyrics)
	require.NoError(t, err)

	assert.Equal(t, "jazz", prompts.gotRecipeMode)
	assert.Equal(t, "Rainy Amsterdam", recipe.Title)
	assert.Equal(t, "custom-text-model", recipe.TextModel)
	assert.Equal(t, "lyria-custom-v1", recipe.AudioModel)
	if assert.NotNil(t, recipe.Seed) {
		assert.Equal(t, seed, *recipe.Seed)
	}
	// 共有結果ではなく複製に付与していること（Seed のポインタが使い回されていない）。
	assert.NotSame(t, models.Seed, recipe.Seed)

	// 歌詞も生成後に付けられ、こちらも複製である。
	require.NotNil(t, recipe.Lyrics)
	assert.Equal(t, "Rainy Amsterdam", recipe.Lyrics.Title)
	assert.NotSame(t, lyrics, recipe.Lyrics)
}

// TestComposeParsesFullRecipe は、レシピ JSON の各項目が取り込まれることを
// 検証します。セクションの timeline はレシピの再利用時に検証されるため、
// 落ちると下流で弾かれます。
func TestComposeParsesFullRecipe(t *testing.T) {
	const rawJSON = `{
		"title": "Lofi Chill",
		"tempo": 70,
		"mood": "relaxed",
		"key": "D minor",
		"vocal_profile": "Japanese female vocal, clear diction",
		"instruments": ["synth"],
		"sections": [
			{"name":"Verse","duration_seconds":30,"start_seconds":0,"end_seconds":30,"prompt":"soft opening groove"}
		]
	}`
	g := newTextGenerator(textResponder(rawJSON), &stubTextPrompts{recipePrompt: "p"})

	recipe, err := g.Compose(context.Background(), AIModels{ComposeMode: "lofi"}, &LyricsDraft{Lyrics: "l"})
	require.NoError(t, err)

	assert.Equal(t, "Lofi Chill", recipe.Title)
	assert.Equal(t, 70, recipe.Tempo)
	assert.Equal(t, "D minor", recipe.Key)
	assert.Equal(t, "Japanese female vocal, clear diction", recipe.VocalProfile)
	assert.Equal(t, []string{"synth"}, recipe.Instruments)
	require.Len(t, recipe.Sections, 1)
	assert.Equal(t, 0, recipe.Sections[0].StartSeconds)
	assert.Equal(t, 30, recipe.Sections[0].EndSeconds)
}

// TestComposeDefaultsComposeMode は、モード未指定時に既定モードでプロンプトを
// 組み立てることを検証します。空文字列のまま渡すと、モードで分岐する呼び出し側の
// プロンプト実装が該当なしで失敗します。
func TestComposeDefaultsComposeMode(t *testing.T) {
	prompts := &stubTextPrompts{recipePrompt: "p"}
	g := newTextGenerator(textResponder(`{"title":"Song"}`), prompts)

	_, err := g.Compose(context.Background(), AIModels{}, &LyricsDraft{Lyrics: "l"})

	require.NoError(t, err)
	assert.Equal(t, defaultComposeMode, prompts.gotRecipeMode)
}

func TestComposeErrorBranches(t *testing.T) {
	tests := []struct {
		name        string
		prompts     *stubTextPrompts
		ai          *fakeGenerator
		wantErr     error
		wantMessage string
	}{
		{
			name:        "プロンプト構築の失敗",
			prompts:     &stubTextPrompts{recipeErr: errors.New("prompt boom")},
			ai:          &fakeGenerator{},
			wantMessage: "failed to build prompt",
		},
		{
			name:        "AI がエラーを返す",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          &fakeGenerator{err: errors.New("ai boom")},
			wantMessage: "compose generation failed",
		},
		{
			name:        "AI が nil レスポンスを返す",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          &fakeGenerator{},
			wantErr:     ErrInvalidResponse,
			wantMessage: "compose response is nil",
		},
		{
			name:        "AI が空文字列を返す",
			prompts:     &stubTextPrompts{recipePrompt: "p"},
			ai:          textResponder(""),
			wantErr:     ErrInvalidResponse,
			wantMessage: "AI returned an empty string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newTextGenerator(tt.ai, tt.prompts)

			_, err := g.Compose(context.Background(), AIModels{}, &LyricsDraft{Lyrics: "l"})

			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
			if tt.wantMessage != "" {
				assert.Contains(t, err.Error(), tt.wantMessage)
			}
		})
	}
}

func TestComposeRejectsNilLyrics(t *testing.T) {
	g := newTextGenerator(&fakeGenerator{}, &stubTextPrompts{})

	_, err := g.Compose(context.Background(), AIModels{}, nil)

	assert.ErrorIs(t, err, ErrNilInput)
}

// TestGenerateJSONTruncatesRawOutputInError は、解釈できなかった生出力が
// エラーメッセージの中で切り詰められることを検証します。レシピ JSON は数 KB に
// なるため、全文を埋め込むとログ 1 行が肥大化します。
func TestGenerateJSONTruncatesRawOutputInError(t *testing.T) {
	long := "{" + strings.Repeat("x", 5000)
	g := newTextGenerator(textResponder(long), &stubTextPrompts{lyricsPrompt: "p"})

	_, err := g.GenerateLyrics(context.Background(), AIModels{}, &CollectedContent{Prompt: "x"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "…(truncated)")
	assert.Less(t, len(err.Error()), len(long), "生出力が丸ごと埋め込まれています")
}

// TestGenerateLyricsDeduplicatesConcurrentCalls は、同一内容の同時呼び出しが 1 回に
// まとまり、それでも各呼び出し元が独立した結果を受け取ることを検証します。
//
// 共有結果をそのまま返すと、1 人が書き換えた内容が他の呼び出し元にも見えます。
func TestGenerateLyricsDeduplicatesConcurrentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{
			block: release,
			resp:  &gemini.Response{Text: `{"title":"Song","theme":"Theme","hook":"h","lyrics":"Words","keywords":["one"]}`},
		}
		g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "lyrics prompt"})
		input := &CollectedContent{Prompt: "same input"}

		const callers = 5
		results := make([]*LyricsDraft, callers)
		errs := make([]error, callers)

		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				results[i], errs[i] = g.GenerateLyrics(context.Background(), AIModels{}, input)
			})
		}

		// バブル内の他のゴルーチンがすべてブロックした時点で Wait が返るため、
		// 「合流したはず」の待ち時間もポーリングも要りません。
		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, 1, ai.callCount(), "同一内容の同時呼び出しがまとめられていません")
		for _, err := range errs {
			require.NoError(t, err)
		}

		// 共有結果ではなく複製が返っていること。
		require.NotSame(t, results[0], results[1])
		results[0].Keywords[0] = "changed"
		assert.Equal(t, "one", results[1].Keywords[0], "内部のスライスが共有されています")
	})
}

// TestGenerateLyricsSeparatesDifferentSeeds は、seed 違いの同時呼び出しが
// 1 回の生成結果を共有しないことを検証します。
//
// seed はプロンプトから導けないのに結果を変えるため、singleflight キーに含め忘れると
// seed による作り分けが黙って効かなくなります。
func TestGenerateLyricsSeparatesDifferentSeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: &gemini.Response{Text: validLyricsJSON}}
		g := newTextGenerator(ai, &stubTextPrompts{lyricsPrompt: "same prompt"})
		input := &CollectedContent{Prompt: "same input"}

		seedA, seedB := int64(1), int64(2)
		var wg sync.WaitGroup
		for _, seed := range []*int64{&seedA, &seedB} {
			wg.Go(func() {
				_, _ = g.GenerateLyrics(context.Background(), AIModels{Seed: seed}, input)
			})
		}

		// 両方が in-flight に入るまで待つ。相乗りしていなければ呼び出しは 2 回になる。
		synctest.Wait()
		got := ai.callCount()

		close(release)
		wg.Wait()

		assert.Equal(t, 2, got, "seed 違いが 1 回の生成結果を共有しています")
	})
}
