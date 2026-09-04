# ✨ Gen Ai Kit

[![CI](https://github.com/shouni/genai-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/genai-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/genai-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/genai-kit)](https://github.com/shouni/genai-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/genai-kit.svg)](https://pkg.go.dev/github.com/shouni/genai-kit)

## 🚀 概要 (About) - genai SDK を公開 API に出さない Vertex AI 専用クライアント。保存も認証情報の配布も引き受けません

**Gen Ai Kit** は、**Google Cloud Vertex AI** 向けの Go ライブラリです。テキスト生成、`gs://` を使った
マルチモーダル入力、参照画像付きの画像生成、Lyria による音楽生成、Veo による動画生成を扱います。
生成物の保存先は決めず、認証は Application Default Credentials に委ねます。

姉妹ライブラリの [go-gemini-client](https://github.com/shouni/go-gemini-client) との違いは 3 つです。
**バックエンドは Vertex AI のみ**（API キー方式はありません）、**参照画像は `gs://` をモデル側に
直接解決させる**（取得もアップロードも起きません）、**画像生成の `imagegen` を内蔵**しています。
詳しくは[使い分け](#-go-gemini-client-との使い分け)にあります。

シグネチャ・フィールド・エラーの一覧は
[pkg.go.dev](https://pkg.go.dev/github.com/shouni/genai-kit) にあります。ここに書くのは、
godoc を読んでも気付けないことだけです。

---

## ✨ 提供機能 (Features)

* **`gemini`**: Vertex AI クライアント。生成の入口は `GenerateText`（テキストのみの最短経路）と
  `Generate`（それ以外すべて）の 2 つだけです。
  * **`genai.Part` を直接受け取る公開 API はありません。** 添付は `Attachment`（`MIMEType` と、
    `Data` か `URI` の片方）で表します。SDK の型を公開面へ出すと、利用側が genai を import する
    理由が復活してしまうためです。設定値の型と定数は別名として再エクスポートしてあるので、
    値を選ぶためだけの import も要りません。
  * **空の添付は黙って読み飛ばします。** 参照画像を「あれば渡す」形で組み立てる呼び出し側が、
    空要素の除去を毎回書かずに済みます。プロンプトが空でも添付があれば送れます（音声だけを
    渡して解析させる用途）。両方空なら `ErrEmptyParts` です。
  * **`Config.HTTPClient` を渡しても認証は失われません。** genai は HTTP クライアントを渡されると
    ADC の検出をスキップし、認証ヘッダ無しで送るため、素の `&http.Client{Timeout: ...}` だと
    全リクエストが 401 `CREDENTIALS_MISSING` になります。本ライブラリが認証情報を付け直し、
    渡したインスタンスは書き換えず複製を使います。
  * **リトライは genai SDK に任せています。** 408 / 429 / 5xx と通信エラーの判定表を持たないので、
    SDK が対象を増やせばそのまま追随します。`Config` で回数と間隔を渡すだけです。
  * **構造化出力でも `CleanJSONResponse` を通してください。** `ResponseSchema` を指定しても、
    モデルは完結した JSON の後ろに説明文を継ぎ足したり、複数行の本文の中に生の改行を入れたりします。
    **どれも応答を返しきったあとの話なので、API の再試行では直りません。**
* **`imagegen`**: 参照画像（`gs://`）付きの画像生成。プロンプト結合・シード採番・既定値・画像抽出を
  引き受けます。詳しくは[参照画像と既定値](#-参照画像と既定値-imagegen)。
* **`music`**: 楽曲構成のデータ型（`Recipe` / `Section` / `LyricsDraft` / `AIModels`）。依存を持たない
  葉パッケージで、レシピを読み書きするだけの下流サービスがワークフロー本体を輸入せずに済みます。
  JSON タグは snake_case で、**保存済みレシピ JSON との互換性の契約**です。
* **`lyria`**: 歌詞生成 → 作曲レシピ生成 → Lyria 音声生成の 3 段。
  * **プロンプト本文を一切持ちません。** 組み立ては `TextPromptBuilder` / `AudioPromptBuilder` を
    注入して決めます。このパッケージでは **`Generator` が AI を呼ぶ側、`Builder` が呼ばない
    組み立て役**を指します。
  * **一括実行の入口は意図的にありません。** 3 段を個別に呼ぶのは、段の間に構造検証などの
    品質ゲートを挟めるようにするためです。
  * `lyria.MusicRecipe` / `MusicSection` / `LyricsDraft` / `AIModels` は `music` の型の別名なので、
    既存の表記もそのまま使えます。
* **`veo`**: Veo 動画生成の投函と完了待ち。**「1 往復ずつ」を `gemini` が、「どう待つか」を `veo` が
  持ちます。**
  * `Submit`（投函だけ）と `Wait`（名前を渡して待ちを再開）に分けて呼べます。実行時間に上限のある
    ジョブ基盤で、投函を済ませて一旦戻る使い方ができます。
  * **投函にはリトライが効き、1 回ごとのポーリングには効きません。** ポーリングの中でさらに
    バックオフを効かせると、設定した間隔とタイムアウトが意味を失うためです。一時的な失敗は
    `WithMaxPollErrors` の回数まで受け流します。
  * **入力系統は併用できません**（image / video / references）。API が確実に拒否する組み合わせは
    送信前に `ErrInvalidVideoInput` で弾きます。
* **`callguard`**: AI 呼び出しへの発射間隔・1 回あたりの上限時間・重複排除（singleflight）。
  * **クォータはプロジェクト単位で、操作の種類ごとではありません。** テキスト生成と画像生成で
    別々に絞っても意味がないため、ワークフロー全体で `Guard` を 1 つ共有し、重複排除の単位
    （`Group`）だけを呼び出しの種類ごとに分けます。
  * **戻り値は相乗りした全員で共有されます。** 呼び出し側が書き換える可能性があるものは
    複製してから返してください。

**genai SDK を import するのは `gemini` だけです。** 上位のパッケージはいずれも `gemini` の
1〜2 メソッドのインターフェースだけを受け取るため、テストでは SDK も GCP 認証も要りません。

---

## 📦 パッケージ構成 (Package Structure)

```text
genai-kit/
├── gemini/      # Vertex AI クライアント。生成・リトライ・レスポンス抽出と、Veo の 1 往復
├── imagegen/    # gs:// 参照画像付きの画像生成（gemini.Generator を注入）
├── music/       # 楽曲構成のデータ型。依存を持たない葉
├── lyria/       # 歌詞 → レシピ → 音声の 3 段
├── veo/         # Veo 動画生成の投函と完了待ち
├── callguard/   # 発射間隔・上限時間・重複排除。依存を持たない葉
└── internal/
    └── poll/    #   veo が使うポーリングの骨格
```

インポートパスはいずれも `github.com/shouni/genai-kit/` を前置します。

---

## 🚦 使い方 (Usage)

```sh
go get github.com/shouni/genai-kit
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/shouni/genai-kit/gemini"
)

func main() {
	ctx := context.Background()

	client, err := gemini.New(ctx, gemini.Config{
		ProjectID:  "your-google-cloud-project-id",
		LocationID: "asia-northeast1",
	})
	if err != nil {
		log.Fatal(err)
	}

	resp, err := client.GenerateText(ctx, "gemini-3.7-flash", "Goで短い俳句を書いて")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Text)
}
```

`ProjectID` と `LocationID` は両方必須です。認証は Application Default Credentials に従うため、
Cloud Run などの環境では API キーをアプリケーションに持たせずに運用できます。ローカルでは
`gcloud auth application-default login` で認証情報を用意してください。

添付付きの生成・画像生成・音楽生成・動画生成の例は
[pkg.go.dev](https://pkg.go.dev/github.com/shouni/genai-kit) にあります。

---

## 🎨 参照画像と既定値 (`imagegen`)

`imagegen` は参照画像付きの画像生成をまとめます。`gemini.Generate` を直接呼ぶのとの違いは、
プロンプトとネガティブプロンプトの結合、シードの自動採番、既定値の補完、レスポンスからの
画像抽出を引き受ける点です。

**参照画像は `gs://` のみです。** Vertex AI は `gs://` をモデル側で解決するため、取得も
アップロードもバイト列の転送も起きません。それ以外の URI は `ErrUnsupportedReference` で
弾きます。**HTTP から取得してインラインで送る経路や、File API を経由する経路はありません。**
それらが要る構成では [gemini-image-kit](https://github.com/shouni/gemini-image-kit) を
使ってください。

未指定時に補われる既定値は次の 2 つです（明示した値は上書きしません。上書きすると、利用側が
安全フィルタを厳しくする手段が無くなるためです）。

- `SafetySettings` → `NewSafetySettings(SafetyBlockNone)`
- `PersonGeneration` → `PersonGenerationAllowAll`（キャラクター生成が主用途のため）

`NegativePrompt` は API のフィールドではなく、`"\n\n[Negative Prompt]\n"` を区切りとして
`Prompt` へ連結して送ります。**この見た目は下流のプロンプト実装が依存している互換性の契約です。**

**`imagegen` は発射間隔も重複排除も持ちません。** クォータはプロジェクト単位なので、
`imagegen.Generator` を `callguard` でデコレートし、テキスト生成と 1 つの `Guard` を共有する形で
ワークフロー層に置いてください。ライブラリごとに独立したレート制限を持たせると、合計が
クォータを超えます。

---

## 🔀 go-gemini-client との使い分け

| | [go-gemini-client](https://github.com/shouni/go-gemini-client) | genai-kit |
| --- | --- | --- |
| バックエンド | Gemini API（API キー）と Vertex AI | **Vertex AI のみ** |
| 参照画像 | File API へ上げてキャッシュする経路を持つ | **`gs://` をモデル側に解決させる**（転送が起きない） |
| 画像生成 | [gemini-image-kit](https://github.com/shouni/gemini-image-kit) へ委譲 | `imagegen` を内蔵 |

API キーを配る必要が無く、参照画像が GCS にあるなら genai-kit です。バックエンドを実行時に
選びたい、あるいは Gemini API の File API に上げた素材を使いたいなら go-gemini-client です。
参照画像を HTTP から取得したり再圧縮したりする必要があるなら、どちらでもなく
gemini-image-kit が担当します。

---

## 🤝 依存関係 (Dependencies)

- [google.golang.org/genai](https://pkg.go.dev/google.golang.org/genai) - Google 公式 SDK（Vertex AI バックエンドで使用）
- [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) - `callguard` の singleflight
- [golang.org/x/time](https://pkg.go.dev/golang.org/x/time) - `callguard` のレート制限

---

## 📜 ライセンス (License)

このプロジェクトは [MIT License](https://opensource.org/licenses/MIT) の下で公開されています。
