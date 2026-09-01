package imagegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExtensionByMIMEType は、保存ファイルの拡張子の引き当てを検証します。
//
// 判定できない MIME type を既定値（.png）へ倒すのは、mimeTypeByPath が空文字列で
// 判定を委ねるのとは逆ですが、実害が違うためです。拡張子の誤りは保存パスの見た目に
// 留まり、生成済みの画像を保存できずに捨てるほうが損失は大きくなります。
func TestExtensionByMIMEType(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"image/png":                ".png",
		"image/jpeg":               ".jpg",
		"image/webp":               ".webp",
		"image/gif":                ".gif",
		"IMAGE/PNG":                ".png",
		"  image/png  ":            ".png",
		"image/png; charset=utf-8": ".png",
		// image/jpg は正式な MIME type ではありませんが、生成モデルの応答や
		// 手書きの設定値では実際に現れます。既定へ倒すと中身と食い違います。
		"image/jpg": ".jpg",
		// 判定できないものは既定値へ倒す。
		"application/octet-stream": ".png",
		"":                         ".png",
		"not a mime type":          ".png",
		// パラメーターが壊れていてもメディアタイプ自体は拾える。
		"image/webp; =broken": ".webp",
	}

	for mimeType, want := range tests {
		t.Run(mimeType, func(t *testing.T) {
			assert.Equal(t, want, ExtensionByMIMEType(mimeType))
		})
	}
}

// TestMimeTypeByPath は、パスや URI の拡張子からの引き当てを検証します。
// 判定できない場合に空文字列を返すのは、誤った型の申告が受け取った側の解釈を
// そのまま壊すためです。
func TestMimeTypeByPath(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"gs://bucket/a.png":  "image/png",
		"gs://bucket/a.jpg":  "image/jpeg",
		"gs://bucket/a.jpeg": "image/jpeg",
		"gs://bucket/a.webp": "image/webp",
		"gs://bucket/a.gif":  "image/gif",
		"gs://bucket/a.PNG":  "image/png",
		"a.png":              "image/png",
		"gs://bucket/a":      "",
		"gs://bucket/a.txt":  "",
		"":                   "",
		// 署名付き URL のようなクエリ付きでも、パス部分の拡張子を見る。
		// 素の filepath.Ext は ".png?X-Goog-Signature=..." を返すため、
		// 最も一般的な URL の形で常に判定不能になります。
		"https://storage.googleapis.com/bucket/a.png?X-Goog-Signature=abc": "image/png",
	}

	for path, want := range tests {
		t.Run(path, func(t *testing.T) {
			assert.Equal(t, want, mimeTypeByPath(path))
		})
	}
}

// TestMIMETablesStayInSync は、2 つの対応表が同じフォーマット集合を扱っていることを
// 検証します。片方だけにフォーマットを足すと、送信時と保存時で扱いが食い違います。
func TestMIMETablesStayInSync(t *testing.T) {
	t.Parallel()

	// mimeTypeByPath が返しうる MIME type は、すべて ExtensionByMIMEType が
	// 既定値以外の拡張子で受けられること。
	pathToMIME := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".webp": "image/webp",
		".gif":  "image/gif",
	}

	for ext, mimeType := range pathToMIME {
		assert.Equal(t, mimeType, mimeTypeByPath("gs://bucket/a"+ext))
		if ext == ".jpg" {
			assert.Equal(t, ".jpg", ExtensionByMIMEType(mimeType))
			continue
		}
		assert.Equal(t, ext, ExtensionByMIMEType(mimeType))
	}
}

func TestIsGCSURI(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"gs://bucket/a.png":         true,
		"GS://bucket/a.png":         true,
		"Gs://bucket/a.png":         true,
		"https://example.com/a.png": false,
		"bucket/a.png":              false,
		"":                          false,
	}

	for uri, want := range tests {
		t.Run(uri, func(t *testing.T) {
			assert.Equal(t, want, isGCSURI(uri))
		})
	}
}
