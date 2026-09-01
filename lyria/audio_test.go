package lyria

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAudioGenerator は、発射間隔なしの audioGenerator を組み立てます。
func newAudioGenerator(ai gemini.Generator, prompts AudioPromptBuilder, converter ReadingConverter) *audioGenerator {
	if converter == nil {
		converter = noopReadingConverter{}
	}
	return &audioGenerator{
		aiClient:          ai,
		prompts:           prompts,
		converter:         converter,
		defaultLyriaModel: "lyria-3",
	}
}

// TestGenerateAudioSendsPromptAndImages は、組み立てたプロンプトと画像が
// そのまま Lyria へ渡ることを検証します。
func TestGenerateAudioSendsPromptAndImages(t *testing.T) {
	ai := audioResponder([]byte{1, 2, 3})
	prompts := &stubAudioPrompts{fullSong: "full prompt"}
	g := newAudioGenerator(ai, prompts, nil)

	recipe := &MusicRecipe{Title: "Song"}
	images := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}

	audio, err := g.GenerateAudio(context.Background(), recipe, images)
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, audio)

	assert.Same(t, recipe, prompts.gotRecipe, "プロンプト構築にレシピがそのまま渡ること")

	call := ai.lastCall(t)
	assert.Equal(t, "full prompt", call.Prompt)
	assert.Equal(t, images, call.Attachments)
	// Lyria はレスポンス MIME type の指定なしで音声を返すため、指定しない。
	assert.Empty(t, call.Opts.ResponseMIMEType)
	assert.NotEmpty(t, call.Opts.SafetySettings)
}

// TestGenerateAudioResolvesModel は、レシピが指定するモデルが既定より優先される
// ことを検証します。
func TestGenerateAudioResolvesModel(t *testing.T) {
	tests := []struct {
		name       string
		audioModel string
		want       string
	}{
		{"未指定なら既定モデル", "", "lyria-3"},
		{"レシピの指定が優先されること", "lyria-custom", "lyria-custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := audioResponder([]byte{1})
			g := newAudioGenerator(ai, &stubAudioPrompts{fullSong: "p"}, nil)

			_, err := g.GenerateAudio(context.Background(),
				&MusicRecipe{Title: "Song", AIModels: AIModels{AudioModel: tt.audioModel}}, nil)

			require.NoError(t, err)
			assert.Equal(t, tt.want, ai.lastCall(t).Model)
		})
	}
}

// TestGenerateAudioAppliesReadingConverterByLanguage は、読み仮名変換が日本語の
// レシピにだけ掛かることを検証します。Lang 未指定は日本語扱いです。
func TestGenerateAudioAppliesReadingConverterByLanguage(t *testing.T) {
	tests := []struct {
		name string
		lang string
		want string
	}{
		{"未指定は日本語扱いで変換される", "", "converted prompt"},
		{"日本語は変換される", LangJapanese, "converted prompt"},
		{"英語は変換しない", LangEnglish, "original prompt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := audioResponder([]byte{1})
			g := newAudioGenerator(ai, &stubAudioPrompts{fullSong: "original prompt"},
				fixedReadingConverter{output: "converted prompt"})

			_, err := g.GenerateAudio(context.Background(),
				&MusicRecipe{Title: "Song", AIModels: AIModels{Lang: tt.lang}}, nil)

			require.NoError(t, err)
			assert.Equal(t, tt.want, ai.lastCall(t).Prompt)
		})
	}
}

// TestGenerateAudioKeepsSeed は、レシピのシードが生成呼び出しへ渡ることを
// 検証します。落とすと、レシピを保存していても同じ曲を作り直せません。
func TestGenerateAudioKeepsSeed(t *testing.T) {
	seed := int64(42)
	ai := audioResponder([]byte{1})
	g := newAudioGenerator(ai, &stubAudioPrompts{fullSong: "p"}, nil)

	_, err := g.GenerateAudio(context.Background(),
		&MusicRecipe{Title: "Song", AIModels: AIModels{Seed: &seed}}, nil)
	require.NoError(t, err)

	got := ai.lastCall(t).Opts.Seed
	if assert.NotNil(t, got) {
		assert.Equal(t, seed, *got)
	}
}

func TestGenerateAudioErrorBranches(t *testing.T) {
	tests := []struct {
		name    string
		ai      *fakeGenerator
		wantErr error
	}{
		{
			name:    "AI がエラーを返す",
			ai:      &fakeGenerator{err: errors.New("lyria boom")},
			wantErr: nil,
		},
		{
			name:    "AI が nil レスポンスを返す",
			ai:      &fakeGenerator{},
			wantErr: ErrNoAudio,
		},
		{
			name:    "音声が 1 件も含まれない",
			ai:      &fakeGenerator{resp: &gemini.Response{Text: "説明だけ"}},
			wantErr: ErrNoAudio,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newAudioGenerator(tt.ai, &stubAudioPrompts{fullSong: "p"}, nil)

			_, err := g.GenerateAudio(context.Background(), &MusicRecipe{Title: "Song"}, nil)

			require.Error(t, err)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestGenerateAudioRejectsNilRecipe(t *testing.T) {
	g := newAudioGenerator(&fakeGenerator{}, &stubAudioPrompts{}, nil)

	_, err := g.GenerateAudio(context.Background(), nil, nil)

	assert.ErrorIs(t, err, ErrNilInput)
}

// TestGenerateAudioDeduplicatesConcurrentCalls は、同一内容の同時呼び出しが 1 回に
// まとまり、それでも各呼び出し元が独立したバイト列を受け取ることを検証します。
func TestGenerateAudioDeduplicatesConcurrentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: &gemini.Response{Audios: [][]byte{{1, 2, 3}}}}
		g := newAudioGenerator(ai, &stubAudioPrompts{fullSong: "full prompt"}, nil)

		seed := int64(7)
		recipe := &MusicRecipe{Title: "Song", AIModels: AIModels{Seed: &seed}}
		images := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}

		const callers = 5
		results := make([][]byte, callers)
		errs := make([]error, callers)

		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				results[i], errs[i] = g.GenerateAudio(context.Background(), recipe, images)
			})
		}

		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, 1, ai.callCount(), "同一内容の同時呼び出しがまとめられていません")
		for _, err := range errs {
			require.NoError(t, err)
		}

		// 共有結果ではなく複製が返っていること。
		results[0][0] = 9
		assert.Equal(t, byte(1), results[1][0], "音声のバイト列が共有されています")
	})
}

// TestGenerateAudioSeparatesDifferentImages は、画像が違う呼び出しが相乗りしない
// ことを検証します。画像は結果を変えるので、キーに含めないと片方の画像で作った
// 音声がもう片方へ返ります。
func TestGenerateAudioSeparatesDifferentImages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: &gemini.Response{Audios: [][]byte{{1, 2, 3}}}}
		g := newAudioGenerator(ai, &stubAudioPrompts{fullSong: "full prompt"}, nil)

		seed := int64(7)
		recipe := &MusicRecipe{Title: "Song", AIModels: AIModels{Seed: &seed}}
		imagesA := []ImagePayload{{Data: []byte("image-a"), MIMEType: "image/png"}}
		imagesB := []ImagePayload{{Data: []byte("image-b"), MIMEType: "image/png"}}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, images := range [][]ImagePayload{imagesA, imagesB} {
			wg.Go(func() {
				_, errs[i] = g.GenerateAudio(context.Background(), recipe, images)
			})
		}

		// 両方が in-flight に入るまで待つ。相乗りしていなければ呼び出しは 2 回になる。
		synctest.Wait()
		got := ai.callCount()

		close(release)
		wg.Wait()

		assert.Equal(t, 2, got, "画像違いが 1 回の生成結果を共有しています")
		for _, err := range errs {
			require.NoError(t, err)
		}
	})
}

// TestImagesHashIgnoresEmptyPayloads は、空の画像がキーに影響しないことを
// 検証します。呼び出し側は画像を「あれば渡す」形で組み立てるため、空要素の
// 有無でキーが変わると重複排除が効かなくなります。
func TestImagesHashIgnoresEmptyPayloads(t *testing.T) {
	filled := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}
	withEmpty := []ImagePayload{
		{MIMEType: "image/png"},
		{Data: []byte("cover"), MIMEType: "image/png"},
	}

	assert.Equal(t, imagesHash(filled), imagesHash(withEmpty))
	assert.NotEqual(t, imagesHash(filled), imagesHash(nil))
	assert.Equal(t, imagesHash(nil), imagesHash([]ImagePayload{}))
}

// TestImagesHashDistinguishesContentAndType は、バイト列と MIME type のどちらが
// 違ってもキーが変わることを検証します。
func TestImagesHashDistinguishesContentAndType(t *testing.T) {
	base := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}
	otherData := []ImagePayload{{Data: []byte("other"), MIMEType: "image/png"}}
	otherType := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/jpeg"}}

	assert.NotEqual(t, imagesHash(base), imagesHash(otherData))
	assert.NotEqual(t, imagesHash(base), imagesHash(otherType))
}

// TestNoopReadingConverterPassesThrough は、既定の変換器が入力を変えないことを
// 検証します。読み仮名変換を注入しない呼び出し側の挙動そのものです。
func TestNoopReadingConverterPassesThrough(t *testing.T) {
	assert.Equal(t, "漢字のまま", noopReadingConverter{}.ToReading("漢字のまま"))
}
