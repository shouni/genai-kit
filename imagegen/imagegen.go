// Package imagegen runs image generation with Vertex AI image models.
//
// Reference images are accepted as gs:// URIs (Request.Images / Request.References) and as
// bytes the caller has already fetched (Request.References). gs:// is resolved by Vertex AI on
// the model side, so nothing is fetched, uploaded or transferred.
//
// Fetching is not this package's job. For http(s) reference images, the caller decides the
// fetch path, timeout and size limit, turns the image into bytes and then passes them in References.
// Abstracting over the choice of fetch method would hand everyone dependencies they do not use
// (an HTTP client, a cache, recompression). There is no path through the Gemini File API.
//
// It has no call guards such as a firing interval, a time limit or deduplication. Quota is
// per project, not per kind of operation, so throttling image generation alone would still let
// text generation use up the same quota. Put the guard in the workflow layer by decorating the
// Generator with callguard and sharing one Guard with text generation.
package imagegen

import (
	"context"

	"github.com/shouni/genai-kit/gemini"
)

// Generator is the image generation entry point callers depend on.
//
// It deliberately has one method, so that a caller's test fake can be written with just this
// one; the same reason gemini.Generator only has Generate.
type Generator interface {
	// Generate generates one image based on reference images (zero or more) and composition parameters.
	Generate(ctx context.Context, req Request) (*Response, error)
}

// These assertions guarantee at compile time that Client satisfies the package's public interfaces.
var _ Generator = (*Client)(nil)

// Client is the image generation implementation.
//
// It has no firing interval, per-call time limit or deduplication. Quota is per project, not
// per kind of operation, so giving each library its own independent rate limit makes the total
// exceed the quota. If a guard is needed, put it in the workflow layer by decorating the Client
// (Generator) with callguard and sharing one Guard with text generation.
type Client struct {
	ai       gemini.Generator
	autoSeed bool
}

// Option is an optional Client setting.
type Option func(*Client)

// WithoutAutoSeed disables automatic seeding of requests that do not specify a seed.
//
// By default, a generation without a Seed gets a random seed picked before sending.
// If seed selection is left to the API, the value is not included in the response, so
// Response.UsedSeed is recorded as 0 and regeneration under the same conditions becomes
// impossible (0 is a valid seed, so it cannot be told apart from "not recorded" either).
// Automatic seeding does not change the randomness of the result — the only difference is
// whether the API or the generator picks the seed.
//
// Use this option only when the caller manages seeds entirely itself.
func WithoutAutoSeed() Option {
	return func(c *Client) {
		c.autoSeed = false
	}
}

// New creates a Client.
//
// A gemini.Generator (the single Generate method) is all ai needs.
// A test fake can be written with that one method too.
//
//	client, err := gemini.New(ctx, gemini.Config{ProjectID: "p", LocationID: "asia-northeast1"})
//	images, err := imagegen.New(client)
func New(ai gemini.Generator, opts ...Option) (*Client, error) {
	if ai == nil {
		return nil, ErrGeneratorRequired
	}

	c := &Client{ai: ai, autoSeed: true}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c, nil
}

// Generate generates one image based on reference images (zero or more) and composition parameters.
// Cancellation follows only the caller's context.
func (c *Client) Generate(ctx context.Context, req Request) (*Response, error) {
	prepared, err := c.prepare(req)
	if err != nil {
		return nil, err
	}

	attachments, err := referenceAttachments(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.ai.Generate(ctx, prepared.model, prepared.prompt, attachments, prepared.opts)
	if err != nil {
		return nil, err
	}
	return prepared.toResponse(resp)
}
