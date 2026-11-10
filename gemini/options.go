package gemini

import (
	"google.golang.org/genai"
)

// PersonGeneration is a custom type for the person generation permission setting.
type PersonGeneration string

const (
	// PersonGenerationUnspecified omits the setting and leaves it to the API default.
	PersonGenerationUnspecified PersonGeneration = ""
	// PersonGenerationAllowAll allows all person generation (recommended for character generation).
	PersonGenerationAllowAll PersonGeneration = "ALLOW_ALL"
	// PersonGenerationAllowAdult allows generating adults only (the SDK default).
	PersonGenerationAllowAdult PersonGeneration = "ALLOW_ADULT"
	// PersonGenerationDontAllow does not allow generating people.
	PersonGenerationDontAllow PersonGeneration = "DONT_ALLOW"
)

// GenerateOptions are the options for each generation request.
//
// Pointer fields distinguish "unset" from "an explicit zero value".
// For example, Temperature 0 (deterministic output) is a meaningful value, so only nil leaves
// it to the SDK default. Use new(expr) to set them.
//
//	opts := gemini.GenerateOptions{Temperature: new(float32(0))}
type GenerateOptions struct {
	SystemPrompt string

	// --- Sampling parameters ---

	// Temperature is the randomness of the output (0 is the most deterministic). nil means the SDK default.
	Temperature *float32
	// TopP is the nucleus sampling threshold. nil means the SDK default.
	TopP *float32
	// TopK is the number of top-K candidates to sample from. nil means the SDK default.
	TopK *float32
	// MaxOutputTokens is the maximum number of tokens to generate. 0 means the SDK default.
	MaxOutputTokens int32
	// StopSequences is a list of strings that stop generation.
	StopSequences []string

	// --- Thinking (Gemini 2.5 and later) ---

	// ThinkingBudget is the maximum number of tokens used for thinking.
	// Setting 0 explicitly disables thinking, keeping latency and cost down.
	// nil leaves it to the model's default.
	//
	// The valid token range differs per model. When used across models,
	// ThinkingLevel is more portable.
	ThinkingBudget *int32
	// ThinkingLevel specifies the amount of thinking as a level (MINIMAL / LOW / MEDIUM / HIGH).
	// Compared with ThinkingBudget's token count, its advantage is being model-independent.
	//
	// ThinkingBudget and ThinkingLevel are mutually exclusive ways to specify it.
	// When both are set, ThinkingLevel takes precedence and ThinkingBudget is not sent.
	ThinkingLevel genai.ThinkingLevel
	// IncludeThoughts, when true, returns the thought summary, available as Response.Thoughts.
	IncludeThoughts bool

	// --- Parameters specific to image generation (Nano Banana / Imagen) ---

	// AspectRatio is the aspect ratio of the generated image (e.g. "16:9"). Empty means the API default.
	AspectRatio string
	// ImageSize is the resolution of the generated image (e.g. "1K"). Empty means the API default.
	ImageSize string
	// Seed is the seed value for reproducibility. A value outside the int32 range results in
	// ErrInvalidSeed. nil leaves it to the API.
	Seed *int64
	// PersonGeneration is the person generation permission setting.
	PersonGeneration PersonGeneration

	// SafetySettings are the safety filter settings. They can be built with NewSafetySettings.
	SafetySettings []*genai.SafetySetting
	// ResponseMIMEType is the expected MIME type of the response
	// ("application/json" / "image/png" / "audio/wav", etc.).
	ResponseMIMEType string
	// ResponseSchema is the structured-output schema. Used together with ResponseMIMEType
	// "application/json", it constrains the model output to the schema at the grammar level,
	// so no extra non-JSON text gets mixed in.
	ResponseSchema *genai.Schema
	// ResponseJSONSchema specifies structured output with a standard JSON Schema.
	// Use it as an alternative when ResponseSchema does not work well with a complex schema,
	// such as one containing $ref.
	//
	// ResponseSchema and ResponseJSONSchema are mutually exclusive ways to specify it.
	// When both are set, ResponseJSONSchema takes precedence and ResponseSchema is not sent.
	ResponseJSONSchema any

	// OutputURIPrefix, when set, makes the API write generated media such as images directly to GCS,
	// and the response returns the destination URI instead of bytes (Vertex AI only).
	// Specify it as "gs://bucket/prefix/"; anything other than gs:// is ErrInvalidOutputURIPrefix.
	//
	// The returned URI goes into the URI of Response.Attachments, and Data is empty. Images / Audios
	// collect bytes only, so with this set they are empty. If the destination is GCS anyway, this
	// saves the bytes round-tripping through the response only for the caller to upload them again.
	//
	// Through an API key, the SDK returns an error before sending, and with InteractionsGenerator it is
	// ErrUnsupportedOption.
	OutputURIPrefix string
}

// HasImageConfig reports whether at least one image-generation-specific parameter is set.
func (o *GenerateOptions) HasImageConfig() bool {
	if o == nil {
		return false
	}
	return o.AspectRatio != "" || o.ImageSize != "" || o.PersonGeneration != PersonGenerationUnspecified
}

// ThinkingLevel is the level of thinking (an alias of genai.ThinkingLevel).
type ThinkingLevel = genai.ThinkingLevel

// Thinking levels.
const (
	// ThinkingUnspecified leaves it to the model's default.
	ThinkingUnspecified ThinkingLevel = genai.ThinkingLevelUnspecified
	// ThinkingMinimal does almost no thinking, minimising latency and cost.
	ThinkingMinimal ThinkingLevel = genai.ThinkingLevelMinimal
	// ThinkingLow does light thinking.
	ThinkingLow ThinkingLevel = genai.ThinkingLevelLow
	// ThinkingMedium does a moderate amount of thinking.
	ThinkingMedium ThinkingLevel = genai.ThinkingLevelMedium
	// ThinkingHigh does the deepest thinking. Quality goes up, but it is slow and expensive.
	ThinkingHigh ThinkingLevel = genai.ThinkingLevelHigh
)

// SafetyThreshold is the safety filter's block threshold (an alias of genai.HarmBlockThreshold).
type SafetyThreshold = genai.HarmBlockThreshold

// Safety filter block thresholds.
const (
	// SafetyBlockNone does not block by safety filter.
	SafetyBlockNone SafetyThreshold = genai.HarmBlockThresholdBlockNone
	// SafetyBlockLowAndAbove blocks low risk and above, the strictest setting.
	SafetyBlockLowAndAbove SafetyThreshold = genai.HarmBlockThresholdBlockLowAndAbove
	// SafetyBlockMediumAndAbove blocks medium risk and above.
	SafetyBlockMediumAndAbove SafetyThreshold = genai.HarmBlockThresholdBlockMediumAndAbove
	// SafetyBlockOnlyHigh blocks high risk only.
	SafetyBlockOnlyHigh SafetyThreshold = genai.HarmBlockThresholdBlockOnlyHigh
)

// Schema is the schema for structured output (GenerateOptions.ResponseSchema) (an alias of genai.Schema).
//
// Unlike ResponseJSONSchema, written as map[string]any, field names are checked at compile
// time. Choose ResponseJSONSchema only when this type cannot express what you need, such as
// JSON Schema's $ref.
//
//	opts := gemini.GenerateOptions{
//		ResponseMIMEType: "application/json",
//		ResponseSchema: &gemini.Schema{
//			Type: gemini.TypeObject,
//			Properties: map[string]*gemini.Schema{
//				"title": {Type: gemini.TypeString},
//			},
//			Required: []string{"title"},
//		},
//	}
type Schema = genai.Schema

// SchemaType is the data type in a schema (an alias of genai.Type).
type SchemaType = genai.Type

// Schema data types.
const (
	// TypeString is a string.
	TypeString SchemaType = genai.TypeString
	// TypeNumber is a floating-point number.
	TypeNumber SchemaType = genai.TypeNumber
	// TypeInteger is an integer.
	TypeInteger SchemaType = genai.TypeInteger
	// TypeBoolean is a boolean.
	TypeBoolean SchemaType = genai.TypeBoolean
	// TypeArray is an array. The element type is specified with Items.
	TypeArray SchemaType = genai.TypeArray
	// TypeObject is an object. Its fields are specified with Properties.
	TypeObject SchemaType = genai.TypeObject
)

// NewSafetySettings returns a slice of SafetySetting applying the same threshold to all four
// standard harm categories (violence, hate, sexual content, dangerous content).
// How to choose the threshold for a given use is left to the caller.
func NewSafetySettings(threshold SafetyThreshold) []*genai.SafetySetting {
	return []*genai.SafetySetting{
		{Category: genai.HarmCategoryHarassment, Threshold: threshold},
		{Category: genai.HarmCategoryHateSpeech, Threshold: threshold},
		{Category: genai.HarmCategorySexuallyExplicit, Threshold: threshold},
		{Category: genai.HarmCategoryDangerousContent, Threshold: threshold},
	}
}
