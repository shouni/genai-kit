package imagegen

import "errors"

// センチネルの文言は英語 + "imagegen: " プレフィックスで統一しています。深いラップの
// 中に埋まってもどのパッケージ由来か判別できるようにするためで、人間向けの文脈は
// ラップする側（fmt.Errorf の %w）が日本語で補います。
var (
	// ErrGeneratorRequired は、New に生成クライアントが渡されなかった場合に返されます。
	ErrGeneratorRequired = errors.New("imagegen: gemini generator is required")
	// ErrModelRequired は、生成リクエストにモデル名が指定されていない場合に返されます。
	ErrModelRequired = errors.New("imagegen: model is required")
	// ErrEmptyPrompt は、プロンプト（ネガティブプロンプト含む）が空の場合に返されます。
	ErrEmptyPrompt = errors.New("imagegen: prompt cannot be empty")
	// ErrUnsupportedReference は、参照画像が gs:// 以外を指している場合に返されます。
	//
	// このパッケージは Vertex AI がモデル側で解決できる gs:// だけを扱います。
	// http(s):// を取得してインラインで送る経路が要る場合は Client のドキュメントを
	// 参照してください。
	ErrUnsupportedReference = errors.New("imagegen: reference must be a gs:// URI")
	// ErrNoImageData は、レスポンスに画像データが含まれていない場合に返されます。
	ErrNoImageData = errors.New("imagegen: no image data found in response")
)
