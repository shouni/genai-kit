# ✨ Gen Ai Kit

[![CI](https://github.com/shouni/genai-kit/actions/workflows/ci.yml/badge.svg)](https://github.com/shouni/genai-kit/actions/workflows/ci.yml)
[![Status](https://img.shields.io/badge/Status-Active-brightgreen)](#)
[![Language](https://img.shields.io/badge/Language-Go-blue)](https://go.dev/)
[![Go Version](https://img.shields.io/github/go-mod/go-version/shouni/genai-kit)](https://go.dev/)
[![GitHub tag (latest by date)](https://img.shields.io/github/v/tag/shouni/genai-kit)](https://github.com/shouni/genai-kit/tags)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/shouni/genai-kit.svg)](https://pkg.go.dev/github.com/shouni/genai-kit)

English | [日本語](README.ja.md)

## 🚀 About - a Vertex AI client that keeps the genai SDK out of its public API. It neither stores results nor hands out credentials

**Gen Ai Kit** is a Go library for **Google Cloud Vertex AI**. It covers text generation, multimodal
input via `gs://`, image generation with reference images, music generation with Lyria, and video
generation with Veo. It does not decide where results are stored, and it leaves authentication to
Application Default Credentials.

**The backend is Vertex AI** (`Config.APIKey` is the one temporary exception; see below),
**`gs://` reference images are resolved by the model itself** (nothing is downloaded or uploaded), and
**image generation (`imagegen`) is built in**.

Signatures, fields and the list of errors are on
[pkg.go.dev](https://pkg.go.dev/github.com/shouni/genai-kit). This page only covers what you would
not notice from reading the godoc.

---

## ✨ Features

* **`gemini`**: the Vertex AI client. There are only two entry points for generation: `GenerateText`
  (the shortest path, text only) and `Generate` (everything else).
  * **No public API takes a `genai.Part` directly.** Attachments are expressed as `Attachment`
    (`MIMEType` plus exactly one of `Data` or `URI`). Exposing SDK types on the public surface would
    bring back the reason for callers to import genai. Configuration value types and constants are
    re-exported as aliases, so no import is needed just to pick a value either.
  * **Empty attachments are silently skipped.** A caller that builds reference images as "pass it if
    there is one" does not have to strip empty elements every time. A request can be sent with an
    empty prompt as long as there is an attachment (for example, passing audio alone to have it
    analysed). If both are empty, the result is `ErrEmptyParts`.
  * **Passing `Config.HTTPClient` does not lose authentication.** When genai is given an HTTP client
    it skips ADC detection and sends requests without an auth header, so a plain
    `&http.Client{Timeout: ...}` makes every request fail with 401 `CREDENTIALS_MISSING`. This library
    re-attaches the credentials, and works on a copy rather than modifying the instance you passed.
  * **Retries are left to the genai SDK.** The library keeps no table of what counts as retryable
    (408 / 429 / 5xx and transport errors), so when the SDK widens its criteria this library follows
    automatically. `Config` only passes the attempt count and intervals.
  * **Decode the body of a structured output with `DecodeJSON[T]`.** Even with `ResponseSchema` set,
    the model appends an explanation after a complete JSON object, or puts raw newlines inside a
    multi-line string. **All of this happens after the response has been returned in full, so an API
    retry does not fix it.** `DecodeJSON` does the empty check, the repair via `CleanJSONResponse`, the
    decode and the excerpt in the error message in one go, and reports failures as
    `ErrEmptyResponse` / `ErrInvalidJSON`. Calling `json.Unmarshal` directly means that on a path that
    forgot the repair, a generation that took minutes is lost entirely.
* **`imagegen`**: image generation with reference images. It takes care of joining prompts, picking
  seeds, defaults and extracting the image. Reference images are either `gs://` (`Images`) or bytes
  the caller has already fetched (`References`). **Fetching is not this package's job** — for an HTTP
  reference image, the caller decides the fetch path, timeout and size limit, and passes the bytes.
* **`music`**: the data types for a song's structure (`Recipe` / `Section` / `LyricsDraft` /
  `AIModels`). A dependency-free leaf package, so a downstream service that only reads and writes
  recipes does not have to import the workflow itself. The JSON tags are snake_case and are **the
  compatibility contract with stored recipe JSON**. It holds only vocabulary that applies to every
  service; caller policy, such as the target model or how lyrics are written, belongs on the caller's
  own type that embeds `Recipe`.
* **`lyria`**: three stages — lyrics generation → composition recipe generation → Lyria audio
  generation.
  * **It holds no prompt text at all.** Lyrics and composition are decided by an injected
    `TextPromptBuilder`, and for audio it receives a prompt assembled by the caller via
    `GenerateAudio(ctx, lyria.AudioRequest{...})`. In this package, **a `Generator` is something that
    calls the AI, and a `Builder` is an assembler that does not**.
  * **There is deliberately no one-shot entry point.** The three stages are called separately so that
    quality gates, such as structure validation, can sit between them.
  * `lyria.MusicRecipe` / `MusicSection` / `LyricsDraft` / `AIModels` are aliases of the `music` types,
    so existing spellings keep working.
* **`veo`**: submitting Veo video generation and waiting for completion. **`gemini` owns "one round
  trip at a time", and `veo` owns "how to wait".**
  * It can be called as `Submit` (submit only) and `Wait` (resume waiting by passing the name). On a
    job platform with a cap on execution time, you can submit and return for now.
  * **Retries apply to the submission, not to each poll.** Adding backoff inside the polling as well
    would make the configured interval and timeout meaningless. Transient failures are tolerated up to
    `WithMaxPollErrors` times.
  * **Input modes cannot be combined** (image / video / references). Combinations the API is certain
    to reject are refused with `ErrInvalidVideoInput` before sending.
* **`callguard`**: firing interval, per-call time limit and deduplication (singleflight) for AI calls.
  * **Quota is per project, not per kind of operation.** Throttling text generation and image
    generation separately achieves nothing, so the whole workflow shares one `Guard`, and only the
    deduplication unit (`Group`) is split per kind of call.
  * **Return values are shared by everyone who joined the call.** Copy anything the caller might
    modify before returning it.

**Only `gemini` imports the genai SDK.** Every higher-level package receives only a one- or
two-method interface from `gemini`, so tests need neither the SDK nor GCP credentials.

---

## 📦 Package Structure

```text
genai-kit/
├── gemini/      # Vertex AI client: generation, retries, response extraction, and Veo's single round trip
├── imagegen/    # image generation with gs:// reference images (gemini.Generator is injected)
├── music/       # data types for a song's structure; a dependency-free leaf
├── lyria/       # three stages: lyrics → recipe → audio
├── veo/         # submitting Veo video generation and waiting for completion
├── callguard/   # firing interval, time limit and deduplication; a dependency-free leaf
└── internal/
    └── poll/    #   the polling skeleton used by veo
```

Every import path is prefixed with `github.com/shouni/genai-kit/`.

---

## 🚦 Usage

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

	resp, err := client.GenerateText(ctx, "gemini-3.7-flash", "Write a short haiku about Go")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(resp.Text)
}
```

`ProjectID` and `LocationID` are both required. Authentication follows Application Default
Credentials, so on environments such as Cloud Run the application can run without holding an API key.
Locally, prepare credentials with `gcloud auth application-default login`.

Examples of generation with attachments, image generation, music generation and video generation are on
[pkg.go.dev](https://pkg.go.dev/github.com/shouni/genai-kit).
**The costly pitfalls are also written in each godoc** — reference image URIs are `gs://` only, with
neither HTTP fetching nor the File API (the `imagegen` package); the `NegativePrompt` separator is a
compatibility contract; defaults are filled in for safety filters and person generation
(`imagegen.Request`); and since it has neither a firing interval nor deduplication, decorate it with
`callguard` and share the `Guard` with text generation (`imagegen.Client`).

---

## ⚠️ `Config.APIKey` is temporary

Setting it switches to the Gemini API backend. This is an exception that exists because the latest
Lyria is not offered on Vertex AI, so music generation alone only runs on the API key path. It will be
removed once Lyria is available on Vertex AI. **Do not choose it for new uses** — the design goal of
this library is that callers never have to know which backend they are on.

---

## 🤝 Dependencies

- [google.golang.org/genai](https://pkg.go.dev/google.golang.org/genai) - Google's official SDK (used with the Vertex AI backend)
- [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) - singleflight for `callguard`
- [golang.org/x/time](https://pkg.go.dev/golang.org/x/time) - rate limiting for `callguard`

---

## 📜 License

This project is released under the [MIT License](https://opensource.org/licenses/MIT).
