package gemini_test

import (
	"errors"
	"fmt"

	"github.com/shouni/genai-kit/gemini"
)

// Generation parameters are specified per request with GenerateOptions.
// Fields whose zero value is meaningful, such as Temperature, are pointers, so pass them with new(expr).
func ExampleGenerateOptions() {
	opts := gemini.GenerateOptions{
		SystemPrompt:    "You are an assistant that answers concisely.",
		Temperature:     new(float32(0)), // 0 = most deterministic
		MaxOutputTokens: 1024,
		StopSequences:   []string{"###"},
	}

	fmt.Println(*opts.Temperature, opts.MaxOutputTokens)
	// Output: 0 1024
}

// Thinking directly affects cost and latency, so it can be disabled explicitly by
// setting ThinkingBudget to 0. When used across models, ThinkingLevel is more portable.
func ExampleGenerateOptions_thinking() {
	// Disable thinking to keep latency down.
	fast := gemini.GenerateOptions{ThinkingBudget: new(int32(0))}

	// A level setting. When combined with ThinkingBudget, this one takes precedence.
	deep := gemini.GenerateOptions{
		ThinkingLevel:   gemini.ThinkingHigh,
		IncludeThoughts: true,
	}

	fmt.Println(*fast.ThinkingBudget, deep.ThinkingLevel, deep.IncludeThoughts)
	// Output: 0 HIGH true
}

// A structured-output schema can be written with this package's aliases, without importing the genai SDK.
func ExampleSchema() {
	opts := gemini.GenerateOptions{
		ResponseMIMEType: "application/json",
		ResponseSchema: &gemini.Schema{
			Type: gemini.TypeObject,
			Properties: map[string]*gemini.Schema{
				"title":    {Type: gemini.TypeString},
				"keywords": {Type: gemini.TypeArray, Items: &gemini.Schema{Type: gemini.TypeString}},
			},
			Required: []string{"title"},
		},
	}

	fmt.Println(opts.ResponseSchema.Type, opts.ResponseSchema.Required)
	// Output: OBJECT [title]
}

// The reason a generation failed can be classified with errors.Is / errors.AsType.
// A block is not fixed by retrying, so the prompt needs revising.
func ExampleAPIResponseError() {
	// In practice you receive the error returned by Generate / GenerateText.
	var err error = &gemini.APIResponseError{
		Reason:  gemini.ErrEmptyResponse,
		Message: "Vertex AI returned an empty response",
	}

	switch {
	case errors.Is(err, gemini.ErrBlocked):
		fmt.Println("blocked: revise the prompt")
	case errors.Is(err, gemini.ErrEmptyResponse):
		fmt.Println("empty: no candidates were returned")
	}

	if apiErr, ok := errors.AsType[*gemini.APIResponseError](err); ok {
		fmt.Println("reason:", apiErr.Reason)
	}

	// Output:
	// empty: no candidates were returned
	// reason: gemini: empty response
}

// CleanJSONResponse strips the Markdown decoration and trailing noise mixed into structured output.
func ExampleCleanJSONResponse() {
	fmt.Println(gemini.CleanJSONResponse("```json\n{\"title\":\"ok\"}\n```"))
	fmt.Println(gemini.CleanJSONResponse(`{"title":"ok"} followed by an explanation`))
	fmt.Println(gemini.CleanJSONResponse(`preamble [1,2,3] postscript`))

	// Output:
	// {"title":"ok"}
	// {"title":"ok"}
	// [1,2,3]
}

// Config is Vertex AI only, and both ProjectID and LocationID are required.
// Authentication follows ADC, so replacing HTTPClient does not drop authentication.
func ExampleConfig() {
	cfg := gemini.Config{
		ProjectID:  "my-project",
		LocationID: "asia-northeast1",
		MaxRetries: 3,
	}

	fmt.Println(cfg.ProjectID, cfg.LocationID, cfg.MaxRetries)
	// Output: my-project asia-northeast1 3
}
