package lyria

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/shouni/genai-kit/callguard"
	"github.com/shouni/genai-kit/gemini"
)

// AudioGenerator は、組み立て済みのプロンプトから音声を生成します。
type AudioGenerator interface {
	GenerateAudio(ctx context.Context, req AudioRequest) (*Track, error)
}

// AudioRequest は 1 回の音声生成の入力です。
//
// レシピではなくプロンプトを受け取るのは、プロンプトの組み立てが呼び出し側の方針で決まるからです。
// 歌詞をどの表記で渡すか、どの生成先向けの構成か、といった判断はレシピの外の情報を要し、
// このパッケージが Recipe を受け取ってビルダーへ回すと、その情報を Recipe に載せるしかなくなります。
type AudioRequest struct {
	// Prompt は Lyria へ渡すプロンプト全文です。空は ErrNilInput です。
	Prompt string
	// Model は使う音声モデルです。空は WithLyriaModel の既定です。
	Model string
	// Seed は Lyria へ渡す seed です。nil は指定なしです。
	Seed *int64
	// Images は添える参照画像です。
	Images []ImagePayload
}

// Track は 1 回の音声生成の結果です。MIME type とテキストは呼び出し側で作り直せないため、
// 音声バイト列と一緒に返します。
type Track struct {
	// Audio は生成された音声バイト列です。
	Audio []byte
	// MIMEType は Audio の MIME type です（"audio/mpeg" など）。
	// 保存時の拡張子や Content-Type を決めるのに使います。
	MIMEType string
	// SungLyrics は、音声と一緒に返されたテキストです。
	//
	// lyria-3.5 はここへ自身が組んだ譜面を返します（実測では区間を [[B1]] [[C2]]、歌唱行を
	// [:] で始める記譜、歌詞は渡した表記のまま）。鳴った音の書き起こしではないので、依頼した
	// 歌詞と突き合わせて分かるのは「モデルが最初から歌う気の無かった行」までです。
	//
	// 空文字は異常ではありません。テキストを返さないモデルや、器楽だけの生成があります。
	SungLyrics string
}

// Clone は、呼び出し元が安全に変更できる複製を返します。
// 生成結果は singleflight で同時呼び出しの間に共有されるため（callguard.Do）、複製せずに
// 返すと一方の書き換えがもう一方へ波及します。
func (t *Track) Clone() *Track {
	if t == nil {
		return nil
	}

	dst := *t
	dst.Audio = slices.Clone(t.Audio)
	return &dst
}

// audioGenerator はプロンプトを Lyria に渡し、音声を生成します。
type audioGenerator struct {
	aiClient          gemini.Generator
	defaultLyriaModel string
	// guard は nil で「制限なし・既定の上限時間」を意味します。
	guard *callguard.Guard
	group callguard.Group
}

// GenerateAudio はプロンプトを 1 回の Lyria 呼び出しで音声化します。
func (g *audioGenerator) GenerateAudio(ctx context.Context, req AudioRequest) (*Track, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("%w: audio prompt", ErrNilInput)
	}

	targetModel := g.defaultLyriaModel
	if req.Model != "" {
		targetModel = req.Model
	}

	promptText := req.Prompt
	images := req.Images
	imageHash := imagesHash(images)
	key := callguard.Key("audio-full", targetModel, promptText, callguard.SeedKey(req.Seed), imageHash)
	track, err := callguard.Do(ctx, &g.group, g.guard, key, func(execCtx context.Context) (*Track, error) {
		resp, err := g.aiClient.Generate(
			execCtx,
			targetModel,
			promptText,
			images,
			audioOptions(req.Seed),
		)
		if err != nil {
			return nil, fmt.Errorf("lyria generation failed (model: %s): %w", targetModel, err)
		}
		audio, ok := firstAudioAttachment(resp)
		if !ok {
			return nil, fmt.Errorf("%w (model: %s)", ErrNoAudio, targetModel)
		}

		return &Track{
			Audio:      audio.Data,
			MIMEType:   audio.MIMEType,
			SungLyrics: resp.Text,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	return track.Clone(), nil
}

// firstAudioAttachment は、レスポンスから最初の音声添付を MIME type ごと取り出します。
//
// Response.Audios ではなく Attachments を見るのは MIME type を保つためです。Audios は
// Attachments を同じ接頭辞判定で絞った部分集合なので、選ばれる要素は変わりません。
func firstAudioAttachment(resp *gemini.Response) (gemini.Attachment, bool) {
	if resp == nil {
		return gemini.Attachment{}, false
	}
	for _, attachment := range resp.Attachments {
		if strings.HasPrefix(attachment.MIMEType, "audio/") {
			return attachment, true
		}
	}

	return gemini.Attachment{}, false
}

// imagesHash は画像ペイロードの内容から singleflight 用のキー部品を作ります。
//
// バイト列のまま長さプレフィックス付き（callguard.WriteHashPart と同じ枠組み）で流すのは、
// callguard.Key へ渡すために文字列化すると数 MB の複製がキー作成のたびに走るためです。
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
