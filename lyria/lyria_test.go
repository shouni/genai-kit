package lyria

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// このパッケージのテストダブルは手書きです。注入している口はどれもメソッドが
// 1〜2 個しかないため、生成ライブラリを挟むより素直な構造体のほうが短く書けます。
// メソッド名を文字列で指定する仕組みだと、gemini 側の改名がコンパイルではなく
// 実行時（期待の不一致）に露見するという実害もあります。
var (
	_ gemini.Generator   = (*fakeGenerator)(nil)
	_ TextPromptBuilder  = (*stubTextPrompts)(nil)
	_ AudioPromptBuilder = (*stubAudioPrompts)(nil)
)

// generateCall は fakeGenerator が受け取った 1 回分の呼び出しです。
type generateCall struct {
	Model       string
	Prompt      string
	Attachments []gemini.Attachment
	Opts        gemini.GenerateOptions
}

// fakeGenerator は gemini.Generator のテストダブルです。
type fakeGenerator struct {
	mu    sync.Mutex
	calls []generateCall

	// respond が非 nil ならそれを使い、無ければ resp / err をそのまま返します。
	respond func(call generateCall) (*gemini.Response, error)
	resp    *gemini.Response
	err     error

	// block が非 nil なら、応答を返す前にこのチャネルが閉じるのを待ちます。
	// singleflight の合流を作るために使います。
	block chan struct{}
}

func (f *fakeGenerator) Generate(_ context.Context, model, prompt string, attachments []gemini.Attachment, opts gemini.GenerateOptions) (*gemini.Response, error) {
	call := generateCall{Model: model, Prompt: prompt, Attachments: attachments, Opts: opts}

	f.mu.Lock()
	f.calls = append(f.calls, call)
	respond, resp, err, block := f.respond, f.resp, f.err, f.block
	f.mu.Unlock()

	if block != nil {
		<-block
	}
	if respond != nil {
		return respond(call)
	}
	return resp, err
}

func (f *fakeGenerator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// lastCall は直近の呼び出しを返します。1 度も呼ばれていなければテストを失敗させます。
func (f *fakeGenerator) lastCall(t *testing.T) generateCall {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls, "AI が 1 度も呼ばれていません")
	return f.calls[len(f.calls)-1]
}

// textResponder は、テキスト生成の応答を返す fakeGenerator を作ります。
func textResponder(text string) *fakeGenerator {
	return &fakeGenerator{resp: &gemini.Response{Text: text}}
}

// audioResponder は、音声を返す fakeGenerator を作ります。
func audioResponder(audio []byte) *fakeGenerator {
	return &fakeGenerator{resp: audioResponse("audio/mpeg", audio, "")}
}

// audioResponse は、Lyria の音声レスポンスをテスト用に組み立てます。
//
// Attachments と Audios を両方埋めるのは gemini 側の変換がそう作るためです。Audios だけの
// レスポンスは実際には返らないので、それを模したフェイクでは MIME type の経路を検証できません。
func audioResponse(mimeType string, audio []byte, text string) *gemini.Response {
	return &gemini.Response{
		Text:        text,
		Audios:      [][]byte{audio},
		Attachments: []gemini.Attachment{{MIMEType: mimeType, Data: audio}},
	}
}

// stubTextPrompts は TextPromptBuilder のテストダブルです。
// 受け取った mode と入力を記録し、固定のプロンプト（またはエラー）を返します。
//
// 記録に mutex を掛けているのは、重複排除のテストが同じスタブを複数のゴルーチンから
// 呼ぶためです（記録の読み出しは合流後なので素の参照で構いません）。
type stubTextPrompts struct {
	lyricsPrompt string
	recipePrompt string
	lyricsErr    error
	recipeErr    error

	mu             sync.Mutex
	gotLyricsMode  string
	gotLyricsInput string
	gotRecipeMode  string
	gotRecipeDraft *LyricsDraft
}

func (s *stubTextPrompts) LyricsPrompt(mode string, input string) (string, error) {
	s.mu.Lock()
	s.gotLyricsMode, s.gotLyricsInput = mode, input
	s.mu.Unlock()

	return s.lyricsPrompt, s.lyricsErr
}

func (s *stubTextPrompts) RecipePrompt(mode string, lyrics *LyricsDraft) (string, error) {
	s.mu.Lock()
	s.gotRecipeMode, s.gotRecipeDraft = mode, lyrics
	s.mu.Unlock()

	return s.recipePrompt, s.recipeErr
}

// stubAudioPrompts は AudioPromptBuilder のテストダブルです。
type stubAudioPrompts struct {
	fullSong string

	mu        sync.Mutex
	gotRecipe *MusicRecipe
}

func (s *stubAudioPrompts) FullSongPrompt(recipe *MusicRecipe) string {
	s.mu.Lock()
	s.gotRecipe = recipe
	s.mu.Unlock()

	return s.fullSong
}

const validLyricsJSON = `{"title":"t","theme":"th","hook":"h","lyrics":"l"}`

// newTestWorkflow は、モデル名を埋めた最小構成の Workflow を作ります。
// 発射間隔は既定で 0（制限なし）です。
func newTestWorkflow(t *testing.T, ai gemini.Generator, text TextPromptBuilder, audio AudioPromptBuilder, extra ...Option) *Workflow {
	t.Helper()

	base := []Option{WithGeminiModel("gemini-flash"), WithLyriaModel("lyria-3")}
	w, err := New(ai, text, audio, append(base, extra...)...)
	require.NoError(t, err)
	return w
}

// TestNewRequiresDependenciesAndModels は、依存とモデル名の欠落を構築時に弾くことを
// 検証します。モデル名に既定値を持たないのは、選択が品質とコストを直接決めるためです。
func TestNewRequiresDependenciesAndModels(t *testing.T) {
	ai := &fakeGenerator{}
	text := &stubTextPrompts{}
	audio := &stubAudioPrompts{}

	tests := []struct {
		name  string
		ai    gemini.Generator
		text  TextPromptBuilder
		audio AudioPromptBuilder
		opts  []Option
	}{
		{"aiClient が nil", nil, text, audio, []Option{WithGeminiModel("g"), WithLyriaModel("l")}},
		{"textPrompts が nil", ai, nil, audio, []Option{WithGeminiModel("g"), WithLyriaModel("l")}},
		{"audioPrompts が nil", ai, text, nil, []Option{WithGeminiModel("g"), WithLyriaModel("l")}},
		{"GeminiModel が未指定", ai, text, audio, []Option{WithLyriaModel("l")}},
		{"LyriaModel が未指定", ai, text, audio, []Option{WithGeminiModel("g")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := New(tt.ai, tt.text, tt.audio, tt.opts...)

			require.ErrorIs(t, err, ErrWorkflowConfig)
			assert.Nil(t, w)
		})
	}

	t.Run("すべて揃っていれば成功する", func(t *testing.T) {
		w, err := New(ai, text, audio, WithGeminiModel("g"), WithLyriaModel("l"))

		require.NoError(t, err)
		require.NotNil(t, w)
	})
}

// TestNewIgnoresNilOption は、nil の Option を渡しても落ちないことを検証します。
// 条件付きでオプションを組み立てる呼び出し側が、分岐を書かずに済みます。
func TestNewIgnoresNilOption(t *testing.T) {
	_, err := New(&fakeGenerator{}, &stubTextPrompts{}, &stubAudioPrompts{},
		WithGeminiModel("g"), nil, WithLyriaModel("l"))

	require.NoError(t, err)
}

// TestWorkflowDelegatesToEachRole は、ファサードの 3 メソッドがそれぞれの役割へ
// 委譲され、テキストは Gemini、音声は Lyria のモデルに向かうことを検証します。
func TestWorkflowDelegatesToEachRole(t *testing.T) {
	ctx := context.Background()

	t.Run("歌詞生成は Gemini モデルへ", func(t *testing.T) {
		ai := textResponder(validLyricsJSON)
		w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "lyrics prompt"}, &stubAudioPrompts{})

		draft, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "input"})

		require.NoError(t, err)
		assert.Equal(t, "l", draft.Lyrics)
		assert.Equal(t, "gemini-flash", ai.lastCall(t).Model)
	})

	t.Run("作曲は Gemini モデルへ", func(t *testing.T) {
		ai := textResponder(`{"title":"Song","tempo":120}`)
		w := newTestWorkflow(t, ai, &stubTextPrompts{recipePrompt: "recipe prompt"}, &stubAudioPrompts{})

		recipe, err := w.Compose(ctx, AIModels{}, &LyricsDraft{Lyrics: "l"})

		require.NoError(t, err)
		assert.Equal(t, "Song", recipe.Title)
		assert.Equal(t, "gemini-flash", ai.lastCall(t).Model)
	})

	t.Run("音声生成は Lyria モデルへ", func(t *testing.T) {
		ai := audioResponder([]byte{1, 2, 3})
		w := newTestWorkflow(t, ai, &stubTextPrompts{}, &stubAudioPrompts{fullSong: "full prompt"})

		track, err := w.GenerateAudio(ctx, &MusicRecipe{Title: "Song"}, nil)

		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, track.Audio)

		call := ai.lastCall(t)
		assert.Equal(t, "lyria-3", call.Model)
		assert.Equal(t, "full prompt", call.Prompt)
	})
}

// TestTextAndAudioHaveSeparateRateGuards は、テキストと音声の発射間隔が独立して
// いることを検証します。別のモデルの別のクォータなので、片方の混雑でもう片方を
// 絞る理由がありません。共有していると、待たされない側まで待たされます。
func TestTextAndAudioHaveSeparateRateGuards(t *testing.T) {
	const textInterval = 50 * time.Millisecond
	const audioInterval = 30 * time.Second

	t.Run("テキストの発射間隔が音声側に影響しないこと", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ai := &fakeGenerator{respond: func(call generateCall) (*gemini.Response, error) {
				if call.Model == "lyria-3" {
					return audioResponse("audio/mpeg", []byte{1}, ""), nil
				}
				return &gemini.Response{Text: validLyricsJSON}, nil
			}}
			w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p"}, &stubAudioPrompts{fullSong: "full"},
				WithTextRateInterval(textInterval), WithRateInterval(0))

			ctx := context.Background()
			start := time.Now()
			_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "first"})
			require.NoError(t, err)
			_, err = w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "second"})
			require.NoError(t, err)

			// 仮想時計なので、待たされた時間は設定値ちょうどに一致する。
			require.Equal(t, textInterval, time.Since(start), "2 回目のテキスト生成は発射間隔ぶん待つはずです")

			// 音声側は制限なしなので、テキスト側の混雑に引きずられない。
			beforeAudio := time.Now()
			_, err = w.GenerateAudio(ctx, &MusicRecipe{Title: "Song"}, nil)
			require.NoError(t, err)
			assert.Zero(t, time.Since(beforeAudio), "音声生成がテキスト側の発射間隔に巻き込まれています")
		})
	})

	t.Run("音声の発射間隔がテキスト側に影響しないこと", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ai := &fakeGenerator{respond: func(call generateCall) (*gemini.Response, error) {
				if call.Model == "lyria-3" {
					return audioResponse("audio/mpeg", []byte{1}, ""), nil
				}
				return &gemini.Response{Text: validLyricsJSON}, nil
			}}
			w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p"}, &stubAudioPrompts{fullSong: "full"},
				WithRateInterval(audioInterval), WithTextRateInterval(0))

			ctx := context.Background()
			// 音声側の枠を 1 つ使っておく。
			_, err := w.GenerateAudio(ctx, &MusicRecipe{Title: "Song"}, nil)
			require.NoError(t, err)

			start := time.Now()
			_, err = w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "first"})
			require.NoError(t, err)
			assert.Zero(t, time.Since(start), "テキスト生成が音声側の発射間隔に巻き込まれています")
		})
	})
}

// TestWorkflowPropagatesRoleErrors は、各段の失敗がファサードを素通りして
// 呼び出し側へ届くことを検証します。段の間に品質ゲートを挟むのは呼び出し側なので、
// ここで握り潰されると判断材料が消えます。
func TestWorkflowPropagatesRoleErrors(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("ai boom")
	ai := &fakeGenerator{err: sentinel}
	w := newTestWorkflow(t, ai, &stubTextPrompts{lyricsPrompt: "p", recipePrompt: "p"}, &stubAudioPrompts{fullSong: "p"})

	_, err := w.GenerateLyrics(ctx, AIModels{}, &CollectedContent{Prompt: "x"})
	assert.ErrorIs(t, err, sentinel)

	_, err = w.Compose(ctx, AIModels{}, &LyricsDraft{Lyrics: "l"})
	assert.ErrorIs(t, err, sentinel)

	_, err = w.GenerateAudio(ctx, &MusicRecipe{Title: "Song"}, nil)
	assert.ErrorIs(t, err, sentinel)
}
