package lyria

import (
	"github.com/shouni/genai-kit/gemini"
	"github.com/shouni/genai-kit/music"
)

// 楽曲構成のデータ型は music パッケージへ分離されています。
//
// 型だけが欲しい下流（動画生成・履歴表示など）がワークフロー本体（レート制限や
// singleflight を伴うこのパッケージ）を輸入せずに済むようにするためです。
// ここでの別名は既存の lyria.MusicRecipe 等の表記をそのまま使い続けられるように
// する互換層で、どちらの名前で書いても同じ型です。
// 型そのものの説明は music 側にあります。ここへ書き写すと必ずずれます。
type (
	// AIModels は music.AIModels の別名です。
	AIModels = music.AIModels
	// LyricsDraft は music.LyricsDraft の別名です。
	LyricsDraft = music.LyricsDraft
	// MusicRecipe は music.Recipe の別名です。
	MusicRecipe = music.Recipe
	// MusicSection は music.Section の別名です。
	MusicSection = music.Section
)

// LangJapanese と LangEnglish は MusicRecipe.Lang に指定できる言語コードです。
const (
	LangJapanese = music.LangJapanese
	LangEnglish  = music.LangEnglish
)

// ImagePayload は、音声生成に添えられる任意のマルチモーダル画像入力です。
//
// gemini.Attachment の別名です。「MIME type とバイト列」という同じ概念を 2 か所で定義すると、
// 呼び出し側が同じ内容を詰め替える羽目になります。別名にしてあるので、既存の
// lyria.ImagePayload{Data: ..., MIMEType: ...} はそのまま書けます。
type ImagePayload = gemini.Attachment
