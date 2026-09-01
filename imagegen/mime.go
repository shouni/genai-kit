package imagegen

import (
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// ExtensionByMIMEType は、画像の MIME type に対応する保存ファイルの拡張子を
// 先頭のドット付きで返します。判定できない MIME type には ".png" を返すため、
// 保存そのものが止まることはありません。Content-Type ヘッダーの値のように
// パラメーター付きの MIME type もそのまま渡せます。
//
// Response.MIMEType をそのまま渡して、生成物の保存先を決めるためのものです。
// 標準の mime.ExtensionsByType を使わないのは、OS の MIME データベース次第で返る
// 拡張子が環境ごとに変わるためです（image/jpeg に ".jpe" が来ることもあります）。
// 保存パスは URL や履歴に残り続けるので、対応表を固定しています。
//
// 判定できない場合に既定値へ倒すのは、mimeTypeByPath が空文字列で呼び出し側に
// 委ねるのとは逆ですが、両者は実害が違います。MIME type の申告は受け取った側の
// 解釈をそのまま決めるので、誤った申告は内容そのものを壊します。一方の拡張子は
// 保存パスの見た目に留まり、配信時の Content-Type は保存側が MIME type から別途
// 付けます。生成済みの画像を保存できずに捨てるほうが損失は大きいためです。
func ExtensionByMIMEType(mimeType string) string {
	trimmed := strings.TrimSpace(mimeType)

	// パラメーターが壊れていても mime.ParseMediaType はメディアタイプ自体を返します
	// (ErrInvalidMediaParameter)。空で返ったときだけ、生の値で判定を試みます。
	mediaType, _, err := mime.ParseMediaType(trimmed)
	if err != nil && mediaType == "" {
		mediaType = strings.ToLower(trimmed)
	}

	switch mediaType {
	case "image/jpeg", "image/jpg":
		// image/jpg は正式な MIME type ではありませんが、生成モデルの応答や手書きの
		// 設定値では実際に現れます。既定の .png へ倒すと中身と拡張子が食い違うため、
		// 綴りの揺れとして扱います。mimeTypeByPath が ".jpg" と ".jpeg" の
		// どちらも受けるのと同じ理由です。
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

// mimeTypeByPath は、パスや URI の拡張子から MIME type を引きます。
// 判定できない場合は空文字列を返します（誤った型を申告するより、サーバー側の
// コンテンツ判定に委ねるほうが安全なため）。
//
// ExtensionByMIMEType と対になる引き当てです。対応フォーマットを増やすときは
// 両方を直してください。
//
// 署名付き URL のようなクエリ付きの URI では、クエリを除いたパス部分の拡張子を
// 見ます。素の filepath.Ext は ".png?X-Goog-Signature=..." を拡張子として返すため、
// 最も一般的な URL の形で常に判定不能になります。
func mimeTypeByPath(rawPath string) string {
	ext := strings.ToLower(filepath.Ext(rawPath))
	if u, err := url.Parse(rawPath); err == nil && u.Path != "" {
		ext = strings.ToLower(path.Ext(u.Path))
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return ""
	}
}

// isGCSURI は、指定された URI が GCS を指すかを判定します。
// スキームの大文字小文字は区別しません。
func isGCSURI(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), "gs://")
}
