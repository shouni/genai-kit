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
	// ErrUnsupportedReference は、参照画像の URI が gs:// 以外を指している場合に返されます。
	//
	// URI で参照できるのは Vertex AI がモデル側で解決できる gs:// だけです。
	// http(s):// の画像は呼び出し側が取得し、バイト列として Request.References へ
	// 渡してください。
	ErrUnsupportedReference = errors.New("imagegen: reference URI must be a gs:// URI")
	// ErrConflictingReferences は、Request.Images と Request.References の両方が
	// 指定された場合に返されます。2 本のリストの間の順序を決める根拠が無く、
	// 参照画像の順序は生成結果を変えるため、黙って連結せずに弾きます。
	ErrConflictingReferences = errors.New("imagegen: Images and References cannot both be set")
	// ErrMissingReferenceMIMEType は、バイト列で渡した参照画像に MIME type が
	// 指定されていない場合に返されます。バイト列からは型が決まらず、誤った申告は
	// 受け取り側の解釈を壊すため、推測せずに要求します。
	ErrMissingReferenceMIMEType = errors.New("imagegen: inline reference requires a MIME type")
	// ErrNoImageData は、レスポンスに画像データが含まれていない場合に返されます。
	ErrNoImageData = errors.New("imagegen: no image data found in response")
)
