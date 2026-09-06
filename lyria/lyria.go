// Package lyria は、歌詞生成・楽曲設計・Lyria による音声生成を束ねる
// 音楽生成ワークフローを提供します。
//
// 3 段（GenerateLyrics / Compose / GenerateAudio）は個別のメソッドとして公開しており、
// 一括実行の入口は意図的にありません。段の間に挟む品質ゲートは製品ごとに違うため、
// 束ねても呼び出し側で分解し直すことになるからです。
//
// 楽曲の型そのものは music パッケージにあります（MusicRecipe などはその別名です）。
package lyria

import (
	"context"
	"fmt"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// Workflow がパッケージ公開インターフェースを満たすことをコンパイル時に保証します。
// これらのアサーションがないと、メソッドシグネチャがドリフトしても
// 下流の利用側がビルドされるまで気付けません。
var (
	_ LyricsGenerator = (*Workflow)(nil)
	_ Composer        = (*Workflow)(nil)
	_ AudioGenerator  = (*Workflow)(nil)
)

// Workflow は、歌詞生成・作曲・音声生成を束ねるファサードです。
type Workflow struct {
	lyrics   LyricsGenerator
	composer Composer
	audio    AudioGenerator
}

// New は、AI クライアントとプロンプト構築の実装を注入して Workflow を作ります。
//
// モデル名は WithGeminiModel / WithLyriaModel で必ず指定してください。
// 既定値を持たないのは、モデルの選択が品質とコストを直接決めるためです。
func New(aiClient gemini.Generator, textPrompts TextPromptBuilder, audioPrompts AudioPromptBuilder, overrides ...Option) (*Workflow, error) {
	opts := applyOptions(overrides...)
	if aiClient == nil {
		return nil, fmt.Errorf("%w: aiClient is required", ErrWorkflowConfig)
	}
	if textPrompts == nil {
		return nil, fmt.Errorf("%w: textPrompts is required", ErrWorkflowConfig)
	}
	if audioPrompts == nil {
		return nil, fmt.Errorf("%w: audioPrompts is required", ErrWorkflowConfig)
	}
	if opts.geminiModel == "" {
		return nil, fmt.Errorf("%w: GeminiModel is required but not set", ErrWorkflowConfig)
	}
	if opts.lyriaModel == "" {
		return nil, fmt.Errorf("%w: LyriaModel is required but not set", ErrWorkflowConfig)
	}

	// 発射間隔はテキスト（Gemini）と音声（Lyria）で別々に持ちます。別のモデルの
	// 別のクォータなので、片方の混雑でもう片方を絞る理由がありません。
	textGuard := callguard.New(
		callguard.WithRateInterval(opts.textRateInterval),
		callguard.WithExecTimeout(opts.execTimeout),
	)
	audioGuard := callguard.New(
		callguard.WithRateInterval(opts.rateInterval),
		callguard.WithExecTimeout(opts.execTimeout),
	)

	textGen := &textGenerator{
		aiClient:     aiClient,
		prompts:      textPrompts,
		defaultModel: opts.geminiModel,
		guard:        textGuard,
	}

	return &Workflow{
		lyrics:   textGen,
		composer: textGen,
		audio: &audioGenerator{
			aiClient:          aiClient,
			prompts:           audioPrompts,
			guard:             audioGuard,
			defaultLyriaModel: opts.lyriaModel,
		},
	}, nil
}

// GenerateLyrics は収集済みコンテンツから歌詞ドラフトを生成します。
func (w *Workflow) GenerateLyrics(ctx context.Context, ai AIModels, input *CollectedContent) (*LyricsDraft, error) {
	return w.lyrics.GenerateLyrics(ctx, ai, input)
}

// Compose は歌詞ドラフトから楽曲レシピを生成します。
func (w *Workflow) Compose(ctx context.Context, ai AIModels, lyrics *LyricsDraft) (*MusicRecipe, error) {
	return w.composer.Compose(ctx, ai, lyrics)
}

// GenerateAudio は楽曲レシピから曲全体の音声を生成します。
func (w *Workflow) GenerateAudio(ctx context.Context, recipe *MusicRecipe, images []ImagePayload) (*Track, error) {
	return w.audio.GenerateAudio(ctx, recipe, images)
}
