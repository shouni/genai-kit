package lyria

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// AudioGenerator は MusicRecipe から音声バイナリを生成します。
type AudioGenerator interface {
	GenerateAudio(ctx context.Context, recipe *MusicRecipe, images []ImagePayload) ([]byte, error)
}

// AudioPromptBuilder は Lyria の音声生成用プロンプトを構築するインターフェースです。
// メソッドは曲そのものではなくプロンプト文字列を返すため、名詞で示します。
type AudioPromptBuilder interface {
	FullSongPrompt(recipe *MusicRecipe) string
}

// ReadingConverter は Lyria に渡すプロンプトを読み上げ向けの表記に変換します。
type ReadingConverter interface {
	ToReading(input string) string
}

// noopReadingConverter は、WithReadingConverter が指定されなかった場合に使われる
// 何もしないデフォルト実装です。入力をそのまま返します。
// 読み仮名変換が必要な場合は、呼び出し側で ReadingConverter の実装を注入してください。
type noopReadingConverter struct{}

// ToReading は入力をそのまま返します。
func (noopReadingConverter) ToReading(input string) string {
	return input
}

// audioGenerator は MusicRecipe を Lyria に渡し、音声バイナリを生成します。
type audioGenerator struct {
	aiClient          gemini.Generator
	prompts           AudioPromptBuilder
	converter         ReadingConverter
	defaultLyriaModel string
	// guard は nil で「制限なし・既定の上限時間」を意味します。
	guard *callguard.Guard
	group callguard.Group
}

// GenerateAudio は MusicRecipe 全体を 1 回の Lyria 呼び出しで音声化します。
func (g *audioGenerator) GenerateAudio(ctx context.Context, recipe *MusicRecipe, images []ImagePayload) ([]byte, error) {
	if recipe == nil {
		return nil, fmt.Errorf("%w: music recipe", ErrNilInput)
	}

	targetModel := g.defaultLyriaModel
	if recipe.AudioModel != "" {
		targetModel = recipe.AudioModel
	}

	promptText := g.prompts.FullSongPrompt(recipe)
	if recipe.IsJapanese() {
		promptText = g.converter.ToReading(promptText)
	}
	imageHash := imagesHash(images)
	key := callguard.Key("audio-full", targetModel, promptText, callguard.SeedKey(recipe.Seed), imageHash)
	audio, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) ([]byte, error) {
		resp, err := g.aiClient.Generate(
			execCtx,
			targetModel,
			promptText,
			images,
			audioOptions(recipe.Seed),
		)
		if err != nil {
			return nil, fmt.Errorf("lyria generation failed (model: %s): %w", targetModel, err)
		}
		if resp == nil || len(resp.Audios) == 0 {
			return nil, fmt.Errorf("%w (model: %s)", ErrNoAudio, targetModel)
		}

		return resp.Audios[0], nil
	})
	if err != nil {
		return nil, err
	}

	return slices.Clone(audio), nil
}

// imagesHash は画像ペイロードの内容から singleflight 用のキー部品を作ります。
//
// 画像はバイト列のまま長さプレフィックス付きでハッシュへ流します。callguard.Key に
// 渡すために文字列へ変換すると、数 MB の複製がキーを作るたびに走るためです。
// 枠組み（長さプレフィックス）は callguard.WriteHashPart と共有しています。
func imagesHash(images []ImagePayload) string {
	hasher := sha256.New()
	for _, image := range images {
		if len(image.Data) == 0 {
			continue
		}

		callguard.WriteHashPart(hasher, []byte(image.MIMEType))
		callguard.WriteHashPart(hasher, image.Data)
	}

	return "images:" + hex.EncodeToString(hasher.Sum(nil))
}
