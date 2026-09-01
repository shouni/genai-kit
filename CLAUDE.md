# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Go library wrapping the official `google.golang.org/genai` SDK for **Vertex AI**, plus generation workflows built on top of it. Six packages plus one internal, no main.

**This module is the only place in the workspace that imports `google.golang.org/genai`.** Downstream repos depend on it precisely so the SDK stays confined here; adding a direct genai call anywhere else defeats that. When a Google AI API is not yet wrapped, add it here rather than hand-rolling REST in the app.

**Vertex AI only.** The Gemini API (API key) backend was removed — `Config` takes `ProjectID`+`LocationID`, auth is ADC, and there is no backend branching left anywhere. Don't reintroduce a backend flag: the point is that no call site has to ask which backend it is on. `SafetyOff` is deliberately not re-exported because Vertex AI rejects it.

**Tests were reintroduced from `go-gemini-client`, this module's predecessor.** Every package has them, CI measures coverage, and `FuzzCleanJSONResponse` runs for 60s per build. The invariants noted below are pinned by tests again — when one of them is wrong, expect a red test rather than a silent regression. Conventions are in the Tests section.

## Commands

```sh
go build ./...
go vet ./...
go test ./...
go test -race ./...                          # singleflight と synctest まわりに効く
test -z "$(gofmt -l .)"                      # CI fails on unformatted code
golangci-lint run                            # config in .golangci.yml

go test ./gemini -run '^$' -fuzz FuzzCleanJSONResponse -fuzztime 60s
```

Note: a locally installed golangci-lint v2.13.1 panics on the `veo` package (staticcheck SA4023 crash, unrelated to this code). Lint the other packages explicitly if you hit it:
`golangci-lint run ./gemini/... ./imagegen/... ./lyria/... ./music/... ./callguard/... ./internal/...`

## Package layout

Dependencies point one way: `gemini` wraps the SDK; `imagegen` / `lyria` / `veo` take a small `gemini` interface by injection; `music` and `callguard` are dependency-free leaves.

- `gemini/` — the Vertex AI client. Generation, retry mapping, response extraction, and the one-round-trip Veo surface (`StartVideo`/`PollVideo`).
- `imagegen/` — reference-image (`gs://`) image generation over `gemini.Generator`: prompt assembly, seed minting, Vertex defaults, image extraction.
- `music/` — the shared song-structure vocabulary (`Recipe`/`Section`/`LyricsDraft`/`AIModels`). A dependency-free leaf so downstream repos that only need the types don't import the whole workflow; `lyria/compat.go` re-exports them under the old names (`lyria.MusicRecipe = music.Recipe`).
- `lyria/` — lyrics → recipe → audio, exposed as three independently callable steps.
- `veo/` — Veo video generation: submits the long-running operation and owns *how to wait* for it.
- `callguard/` — rate interval + per-execution timeout + singleflight dedup for expensive AI calls. Imports only `x/sync` and `x/time`.
- `internal/poll` — the polling skeleton behind `veo.Client.Wait`.

### File naming

Each package's same-named file (`gemini.go`, `veo.go`, `lyria.go`, `imagegen.go`) holds the package doc, the central type, and its constructor. There are deliberately **no `types.go` / `interfaces.go` files** — an interface lives next to the code that implements or consumes it (e.g. `gemini.Generator` in `generate.go`, `lyria.AudioGenerator` in `audio.go`). `errors.go` per package is the one grouping-by-kind exception, since sentinels are a vocabulary. `gemini/genai.go` is the adapter layer over the SDK; `gemini/jsonclean.go` is `CleanJSONResponse`.

## Architecture

### gemini package

- `Client` wraps the SDK behind two small internal interfaces (`modelClient`, `videoClient` in `genai.go`). Those seams exist so the package can be exercised without the network or GCP credentials; keep them.
- **Retry belongs to the genai SDK, not to this package.** `Config.retryOptions()` maps `MaxRetries`/`InitialDelay`/`MaxDelay` onto `genai.HTTPRetryOptions`; `DisableRetry` returns nil, which the SDK reads as a single attempt. Do not re-copy the SDK's status table (408/429/5xx plus transport failures) into this package. `Jitter` is set to half the initial delay because the SDK default only adds U(0, 1s). Verified against genai v1.71.0: `Attempts` is the total including the first try, and a request's non-nil `RetryOptions` **replaces** the client's wholesale — which is exactly what `noRetryHTTPOptions()` relies on.
- **Config**: `ProjectID` and `LocationID` are both required; `validate` splits "neither set" (`ErrConfigRequired`) from "one set" (`ErrIncompleteVertexConfig`) because those are different mistakes — a forgotten config versus an empty env var.
- **`Config.HTTPClient` is the same field as the credentials, and `toClientConfig` exists to hide that.** genai skips ADC detection entirely when `ClientConfig.HTTPClient` is non-nil and then uses the given client as-is, so a plain `&http.Client{Timeout: ...}` sends no Authorization header and every call fails with 401 CREDENTIALS_MISSING — choosing a timeout silently gives up the auth. `toClientConfig` calls `UseDefaultCredentials` to reattach it, on a shallow copy so the caller's client is not mutated (that call rewrites `Transport`). This reached production as a live 401 once.
- **Public interfaces are exactly two** (`Generator`, `VideoGenerator`), neither mentioning genai types. Streaming, token counting, the File API (`FileManager`), `BackendInspector`/`Model`, and the genai-typed `GenerateWithParts`/`GenerateContentStream` family were all deleted after surveys showed no consumers; git history has them.
- **All public generation entry points are genai-free.** `Generate` takes a prompt plus `[]Attachment` (`MIMEType` + either `Data` or `URI`); `GenerateText` is the no-options text shortcut. `attachmentParts` in `attachment.go` is the one place that converts; it drops empty attachments (callers assemble optional images as "pass it if we have it"), rejects `Data`+`URI` together, and requires a MIME type only for inline data since a URI's type can be left to the server.
- **`Attachment.validateSource` is the single definition of "Data and URI are exclusive".** It takes the sentinel to wrap because the same misuse classifies differently by path — `ErrInvalidAttachment` for generation input, `ErrInvalidVideoInput` for video input — and downstream `errors.Is` checks depend on that split.
- **`Response.Attachments` exists so callers can know the MIME type of returned media.** `Images`/`Audios` are bytes only, so deciding a file extension or Content-Type used to mean walking the raw genai response.
- **`Schema`/`SchemaType`, `SafetyThreshold`, `ThinkingLevel` are aliases of the genai types**, with the constants re-exported. Structured output was the last thing forcing downstream repos to import genai directly, and an alias closes it at zero maintenance cost — it is the same type, so existing `*genai.Schema` values still pass.
- **`GenerateOptions` uses pointers where zero is meaningful** (`Temperature`, `TopP`, `TopK`, `ThinkingBudget`) — `Temperature: 0` means deterministic, not unset. Use `new(expr)`. `ThinkingConfig` is only sent when something is actually specified, so the model's default thinking behavior isn't silently overridden. `ThinkingLevel` beats `ThinkingBudget` and `ResponseJSONSchema` beats `ResponseSchema` when both are set, rather than sending a combination whose precedence is undefined.
- **Neither of those is deprecated**, despite appearances: the SDK's `GenerationConfig` (the batch/tuning payload type) marks its `ResponseSchema`/`ResponseMIMEType` deprecated, but `GenerateContentConfig` — the type this package actually sends — has zero deprecation markers. Don't "fix" our usage based on the wrong struct.
- **Errors** live in `errors.go`. `APIResponseError` carries a `Reason` sentinel (`ErrBlocked` / `ErrEmptyResponse`) plus the `FinishReason`, and `Unwrap` returns the sentinel so `errors.Is` classifies it.
- **`genai.FinishReason` has two "unset" values.** Its Go zero value is `""`, but the SDK constant `FinishReasonUnspecified` is the string `"FINISH_REASON_UNSPECIFIED"`. Comparing only against the constant misclassifies responses that carry no finish reason as blocked. Always go through `isUnsetFinishReason` / `isBlockedFinishReason`. Candidate traversal is centralized in `firstCandidate`/`candidateParts` (response.go) — the server can return nil candidate slots and nil parts, and per-extractor nil checks drifted apart once before.
- **`extractText` concatenates all non-`Thought` text parts.** Thinking-enabled models return the thought summary as a `Thought: true` part *before* the answer, and long answers can be split across parts — returning the first non-empty part gives you the wrong text. Thought parts surface separately as `Response.Thoughts`.
- **`CleanJSONResponse`'s invariant**: if it changes the input at all, the result must be valid JSON — otherwise it leaves callers worse off than the raw string. It used to be fuzzed for 60s in CI, which is coverage that hand-reading does not replace; treat edits here as the highest-risk change in the module. The in-string repair is a **single pass** (`escapeStringLiterals`): lone backslashes and raw control characters are fixed in one traversal, because splitting it in two makes the order load-bearing — escaping a raw newline *adds* a backslash, so a lone-backslash pass running afterwards doubles the escape it just wrote.

### veo package

- **The split between `gemini` and `veo` is "one round trip" vs "how to wait".** `gemini.StartVideo`/`PollVideo` own the genai client and do exactly one call each; `veo.Client` owns the polling interval, the overall timeout, and how many consecutive poll failures to absorb. `veo` exposes both halves — `Submit` returns the operation name without waiting, `Wait` resumes from a name — so a caller splitting the two across job runs never has to reach past `veo` into `gemini`. `Generate` is the two together, short-circuiting when the submission response already came back `Done`.
- **`PollVideo` deliberately opts out of retries via `noRetryHTTPOptions()`, while `StartVideo` keeps them.** Polling is already a retry loop; nesting the SDK's backoff (default 30s initial / 120s max here) inside it makes a single poll take tens of seconds and silently invalidates the caller's interval and timeout. Submission is the opposite case — a 429 there loses a whole video. Don't "fix" the asymmetry.
- `veo` never imports genai. Its `Request`/`Reference`/`Media` are aliases of `gemini.VideoRequest`/`VideoReference`/`Attachment`, so there is no parallel type set to keep in sync.
- **Input combinations are validated before sending** in `VideoRequest.buildSource`: Veo cannot combine `video` with `image`/`referenceImages`, and `lastFrame` is only valid alongside `image`. These are API-level facts, not policy — the *choice* of which mode to use belongs to the caller.
- **`Request.ExtraBody` is the escape hatch for a missing *field*, not a missing *endpoint***. It adds keys to the body of a call the SDK already makes. When the SDK has no client for the endpoint at all, ExtraBody is useless.
- **ExtraBody cannot reach inside an array, and failing that quietly is destructive.** genai merges it with `recursiveMapMerge`, which recurses only when both sides are `map[string]any`; for any other type it overwrites. Vertex bodies are `{"instances": [...], "parameters": {...}}`, so `ExtraBody: {"instances": [...]}` replaces the whole array and takes the prompt and image inputs with it. `Request.ModifyRequestBody` (genai's `ExtrasRequestProvider`) receives the assembled body *after* that merge and is the only way to add a key inside `instances[0]`.
- Operation-level failure is reported on `VideoOperation.Failure` (wrapping `ErrVideoGenerationFailed`), not as the method's error: fetching the operation succeeded, so a transport error would misclassify it.

### internal/poll

The polling skeleton: fire the first attempt with no interval wait, then one attempt per interval, with the deadline covering the in-flight attempt and a tolerance for consecutive transient failures. **The deadline has to cover the attempt itself**: genai's default HTTP client is `&http.Client{}` with no timeout of its own, so a call that never answers never yields to a timeout branch that only runs between attempts. **`Wait` polls once immediately before starting the ticker** — the resume path usually finds the operation already finished, and waiting one interval first was pure dead time.

It has one caller since the File API was removed. It stays `internal` because a public "generic polling" API would grow retry and backoff knobs its caller doesn't want. `veo` supplies the interval, timeout, tolerance, `Subject`, and `ErrPollFailed` as the sentinel to wrap.

### imagegen package

- **Ported from the separate `gemini-image-kit` repo, `gs://`-only.** That repo still exists with the full surface (HTTP fetch, size caps, JPEG recompression, File API upload with a cache); this port kept only what a `gs://` reference needs. Vertex AI resolves `gs://` server-side, so a reference costs no fetch, no upload, and no byte transfer — turning reference resolution into pure string work.
- **That is why there is no resolver abstraction here.** `gemini-image-kit`'s `ReferenceResolver`/`ResolverChain`/`GCSResolver`/`FetchResolver` existed to let the caller choose *how* to send a reference. With one option left the injection point is ceremony, and its dependencies (`ContentReader`, `Downloader`, `ImageCacher`, fetch timeouts, byte caps, compression) go with it. The parallel-resolution machinery went too: with no I/O, a `WaitGroup` plus a cancel context bought nothing over a loop. If a caller needs HTTP references, point them at `gemini-image-kit` rather than reintroducing the abstraction.
- `Client` is the struct, `Generator` the one-method interface it satisfies — the same split as `gemini.Client`/`gemini.Generator`.
- **Seeds are minted before sending, not left to the API.** The API does not return the seed it chose, so `Response.UsedSeed` would record 0 — indistinguishable from a real seed of 0 — and the run stops being reproducible. `WithoutAutoSeed()` opts out.
- **`SafetySettings` and `PersonGeneration` defaults fill only when unset.** Overwriting unconditionally would take away the caller's ability to make the safety filter *stricter*.
- **`negativePromptSeparator` is a compatibility contract.** The negative prompt is not an API field — it is concatenated onto the prompt under a `[Negative Prompt]` heading, and downstream prompt builders match on that exact rendering.
- `ExtensionByMIMEType` (public, for save paths) defaults to `.png` while `mimeTypeByPath` (internal, for the attachment hint) returns `""` when it cannot tell. The asymmetry is deliberate: a wrong MIME type declaration corrupts how the receiver reads the bytes, whereas a wrong extension only affects the save path's appearance. Keep the two tables in sync when adding a format.

### lyria package

- `Workflow` is a facade over three roles: `LyricsGenerator`/`Composer` (both implemented by `textGenerator`) and `AudioGenerator` (`audioGenerator`). Prompt construction is injected by the caller via `TextPromptBuilder` and `AudioPromptBuilder` — **this library contains no prompt text.**
- **Naming rule inside this package: `Generator` calls the AI, `Builder` does not.** That is why the prompt interfaces are `TextPromptBuilder{LyricsPrompt, RecipePrompt}` and `AudioPromptBuilder{FullSongPrompt}` — noun-form methods returning a prompt string, so they don't read like they generate lyrics.
- **There is deliberately no all-in-one `Run`.** One existed and no caller used it: callers invoke the three steps themselves so they can gate between them (verify the composed recipe against the compose mode's declared structure and recompose with a shifted seed on failure, then measure and optionally review the audio). Those gates are per-product, so a bundled entry point only gets decomposed again at the call site.
- `Compose` attaches `AIModels` and `Seed` to the recipe *after* generation — the recipe schema omits them on purpose so the model never invents them. Drop the attachment and a seeded, reproducible run silently stops being reproducible, with no error anywhere.
- Text generation shares one generic pipeline: `generateJSON[T]` in `text.go` (singleflight → Gemini call with JSON MIME type + `ResponseSchema` from `schemas.go` → `gemini.CleanJSONResponse` → unmarshal).
- **Singleflight + clone pattern**: identical concurrent requests are deduplicated via `callguard.Do`, which detaches from the caller's context. Because results are shared across callers, every public method must return a **clone** (`music.Recipe.Clone`/`LyricsDraft.Clone`, `slices.Clone`) and must not write caller-specific data into the shared result.
- **Everything that changes the output must be in the singleflight key.** `seed` in particular: it is passed to the generate call but is *not* implied by the prompt, so omitting it makes concurrent different-seed calls share one result. Use `callguard.SeedKey`. Hash framing is unified in `callguard.WriteHashPart` (length-prefixed) — don't invent a second framing; `imagesHash` stays local only because it hashes raw image bytes, which `callguard.Key`'s string parts would copy.
- Text and audio get **separate** `callguard.Guard`s — different models, different quotas, so congestion on one must not throttle the other.
- `AIModels` is embedded in `music.Recipe`, so its fields are flattened into recipe JSON — it carries explicit snake_case tags to match the rest of the struct. Downstream services persist recipes to GCS in this exact shape, so the JSON wire format is a compatibility contract.

### callguard package

- **The rate-limit wait happens outside the exec timeout.** lyria used to call `limiter.Wait(execCtx)` inside the shared run, so a queued call could fail with `rate: Wait(n=1) would exceed context deadline` purely from congestion. Don't move the wait back inside.
- **The execution context is detached inside the shared closure** (`context.WithoutCancel`), not outside it. Detaching outside means the leader's `defer cancel()` kills the shared run when the leader returns early, taking every piggybacking caller with it.
- **`WithExecTimeout` has no "unlimited".** The shared run is detached from every caller, so this is the only thing that can stop it; unlimited means one hung call holds a goroutine and blocks the same key forever.
- A nil `*Guard` is valid and means "no rate interval, default exec timeout", so callers can pass one through without branching.
- Typed decorators do not belong here — what to wrap is the caller's business, and putting them here grows one method per downstream interface.

## Tests

Ported from `go-gemini-client` (this module's predecessor) and adapted to the Vertex-only surface; `imagegen` is net-new because that repo has no equivalent package.

- **Test doubles are hand-written — never generated, never `testify/mock`.** Every injection point here is one or two methods (`gemini.Generator`, `gemini.VideoGenerator`, the internal `modelClient` / `videoClient`, lyria's prompt builders), so a plain struct that records its calls is shorter than the mock setup and is checked by the compiler. `testify/mock` matches method names as strings, which is how the predecessor's `GenerateWithAttachments` → `Generate` rename left tests that still built and failed at run time. Each fake lives next to the seam it stands in for: `gemini/genai_test.go`, `veo/veo_test.go`, `lyria/lyria_test.go`, `imagegen/imagegen_test.go`.
- **The assertion style splits by layer.** `gemini`, `veo`, `internal/poll` and `music` use plain `testing` with table-driven subtests. `callguard`, `lyria` and `imagegen` use testify — `require` by default, `assert` only where the test can keep going after a failure. Don't write `assert.NoError` and then dereference the result; that turns a readable failure into a nil panic further down.
- **`testing/synctest` covers everything time-dependent** — rate intervals, exec timeouts, poll intervals, deadlines. Inside a bubble the clock is virtual, so elapsed time equals the configured value exactly and assertions compare with `==` rather than a tolerance. `synctest.Wait()` is also how the singleflight tests know every caller has joined the in-flight call, with no polling and no sleeps.
- **One test file per source file**, matching the source-naming rule. There is no shared `helpers_test.go`; the fuzz target sits in `gemini/jsonclean_test.go` beside the function it fuzzes.
- Tests needing real ADC (`gemini.New` on the success path, `toClientConfig` attaching credentials to a supplied `HTTPClient`) go through `skipWithoutGCPCredentials` and skip on CI. Everything else runs with no network and no GCP.
- `go test -race` is worth running: `callguard` and `lyria` are the concurrency surface, and the shared prompt stubs carry a mutex for exactly that reason.

## Conventions

- **All comments are Japanese**, across every package. Sentinel error text is English with a package prefix (`gemini:` / `veo:` / `lyria:` / `imagegen:`) so a deeply wrapped error still names its origin; the human-facing context added by `fmt.Errorf` wrapping stays Japanese.
- **No Markdown in Go comments.** godoc has no bold, so `**…**` reaches pkg.go.dev verbatim on every exported doc. Say it in the first sentence of the paragraph instead. Markdown stays fine in `.md` files, this one included.
- The module targets Go 1.27. In use: `new(expr)` for the pointer-valued options, generic methods (`textGenerator.generateJSON[T]`, `poll.Loop.Run[T]`), `math/rand/v2`, and the `slices`/`max` builtins. The public error contract is built for `errors.AsType[*gemini.APIResponseError]` — it appears in the doc examples rather than in the module's own code, so keep `APIResponseError` and its `Unwrap` shaped for it.
- **`encoding/json/v2` is deliberately not adopted.** Every unmarshal in this module reads model output, which is exactly where v1's leniency is load-bearing: v2 matches member names case-sensitively, so a model that answers `"Title"` for a `json:"title"` field produces a zero-valued struct **and no error at all** — silent data loss, not a failure the caller can retry. v2 also rejects duplicate names and invalid UTF-8 that v1 accepts, which fights `CleanJSONResponse`'s whole purpose.
- Update README.md when public API (Config fields, GenerateOptions, sentinel errors, interfaces) changes — it documents them in tables, one `##` chapter per package, in the same order as the package table. Sample code names concrete models; refresh them when the current generation moves on.
