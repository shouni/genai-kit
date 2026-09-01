package veo

import "errors"

// 入力・結果に関するセンチネルエラーです。呼び出し側は errors.Is で判定し、
// 通信エラーとは異なる制御（プロンプトの見直しなど）を選べます。
var (
	// ErrGeneratorRequired は、New に nil の生成クライアントが渡された場合に返されます。
	ErrGeneratorRequired = errors.New("veo: video generator is required")

	// ErrMissingOperationName は、完了待ちに必要なオペレーション名が無い場合に
	// 返されます。未完了のオペレーションは必ず名前を持つため、通常は起こりません。
	ErrMissingOperationName = errors.New("veo: operation has no name to poll")

	// ErrNoVideoGenerated は、オペレーションは成功で完了したのに動画が1本も
	// 返らなかった場合に返されます。安全性ポリシーによる除外が典型例で、
	// その場合は理由がエラーメッセージに含まれます。再試行では解決しません。
	ErrNoVideoGenerated = errors.New("veo: no video was generated")

	// ErrPollFailed は、生成状況の確認が続けて失敗し、完了を待てなくなった場合に
	// 返されます。最後に発生した原因が Unwrap で辿れます。
	ErrPollFailed = errors.New("veo: polling for completion failed")
)
