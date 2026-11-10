package lyria

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/shouni/genai-kit/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAudioGenerator builds an audioGenerator with no firing interval.
func newAudioGenerator(ai gemini.Generator) *audioGenerator {
	return &audioGenerator{
		aiClient:          ai,
		defaultLyriaModel: "lyria-3",
	}
}

// TestTrackCloneIsIndependent checks that modifying a cloned Track does not spill over to the
// original. Generation results are shared between concurrent calls through singleflight.
func TestTrackCloneIsIndependent(t *testing.T) {
	src := &Track{Audio: []byte{1, 2, 3}, MIMEType: "audio/mpeg", SungLyrics: "sung"}

	dst := src.Clone()
	dst.Audio[0] = 9
	dst.MIMEType = "audio/wav"

	assert.Equal(t, byte(1), src.Audio[0])
	assert.Equal(t, "audio/mpeg", src.MIMEType)
	assert.Equal(t, "sung", dst.SungLyrics)
	assert.Nil(t, (*Track)(nil).Clone())
}

// TestGenerateAudioSendsPromptAndImages checks that the caller's prompt and images reach
// Lyria as is.
func TestGenerateAudioSendsPromptAndImages(t *testing.T) {
	ai := audioResponder([]byte{1, 2, 3})
	g := newAudioGenerator(ai)

	images := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}

	track, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "full prompt", Images: images})
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, track.Audio)

	call := ai.lastCall(t)
	assert.Equal(t, "full prompt", call.Prompt)
	assert.Equal(t, images, call.Attachments)
	// Lyria returns audio without a response MIME type, so none is specified.
	assert.Empty(t, call.Opts.ResponseMIMEType)
	assert.NotEmpty(t, call.Opts.SafetySettings)
}

// TestGenerateAudioResolvesModel checks that a model specified by the caller takes
// precedence over the default.
func TestGenerateAudioResolvesModel(t *testing.T) {
	tests := []struct {
		name       string
		audioModel string
		want       string
	}{
		{"default model when unspecified", "", "lyria-3"},
		{"caller's choice takes precedence", "lyria-custom", "lyria-custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := audioResponder([]byte{1})
			g := newAudioGenerator(ai)

			_, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "p", Model: tt.audioModel})

			require.NoError(t, err)
			assert.Equal(t, tt.want, ai.lastCall(t).Model)
		})
	}
}

// TestGenerateAudioReturnsMIMETypeAndSungLyrics checks that what the response carried reaches
// the caller, not just the audio bytes. Otherwise the MIME type would have to be guessed from the
// bytes (WAV and MP3 get confused), and the score text could not be recovered at all.
func TestGenerateAudioReturnsMIMETypeAndSungLyrics(t *testing.T) {
	ai := &fakeGenerator{resp: audioResponse("audio/mpeg", []byte{1, 2, 3}, "[[V1]]\n[:] sung line")}
	g := newAudioGenerator(ai)

	track, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "p"})

	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, track.Audio)
	assert.Equal(t, "audio/mpeg", track.MIMEType)
	assert.Equal(t, "[[V1]]\n[:] sung line", track.SungLyrics)
}

// TestGenerateAudioPicksTheFirstAudioAttachment checks that only audio is picked even when
// non-audio attachments are mixed in. Since the sorting is done here, getting it wrong would return an image as audio.
func TestGenerateAudioPicksTheFirstAudioAttachment(t *testing.T) {
	ai := &fakeGenerator{resp: &gemini.Response{
		Attachments: []gemini.Attachment{
			{MIMEType: "image/png", Data: []byte("cover")},
			{MIMEType: "audio/wav", Data: []byte{1, 2, 3}},
			{MIMEType: "audio/mpeg", Data: []byte{9}},
		},
	}}
	g := newAudioGenerator(ai)

	track, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "p"})

	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3}, track.Audio)
	assert.Equal(t, "audio/wav", track.MIMEType)
}

// TestGenerateAudioKeepsSeed checks that the specified seed reaches the generation call.
// If it were dropped, the same song could not be recreated even with the recipe saved.
func TestGenerateAudioKeepsSeed(t *testing.T) {
	seed := int64(42)
	ai := audioResponder([]byte{1})
	g := newAudioGenerator(ai)

	_, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "p", Seed: &seed})
	require.NoError(t, err)

	got := ai.lastCall(t).Opts.Seed
	if assert.NotNil(t, got) {
		assert.Equal(t, seed, *got)
	}
}

func TestGenerateAudioErrorBranches(t *testing.T) {
	tests := []struct {
		name    string
		ai      *fakeGenerator
		wantErr error
	}{
		{
			name:    "AI returns an error",
			ai:      &fakeGenerator{err: errors.New("lyria boom")},
			wantErr: nil,
		},
		{
			name:    "AI returns a nil response",
			ai:      &fakeGenerator{},
			wantErr: ErrNoAudio,
		},
		{
			// Audio generation returning only text is the case where the lyrics could be written but no sound produced.
			// Returning an empty Track would let the caller carry 0 bytes of audio all the way to publishing.
			name:    "no audio at all",
			ai:      &fakeGenerator{resp: &gemini.Response{Text: "description only"}},
			wantErr: ErrNoAudio,
		},
		{
			name:    "only non-audio attachments",
			ai:      &fakeGenerator{resp: &gemini.Response{Attachments: []gemini.Attachment{{MIMEType: "image/png", Data: []byte("cover")}}}},
			wantErr: ErrNoAudio,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newAudioGenerator(tt.ai)

			track, err := g.GenerateAudio(context.Background(), AudioRequest{Prompt: "p"})

			require.Error(t, err)
			assert.Nil(t, track)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			}
		})
	}
}

func TestGenerateAudioRejectsEmptyPrompt(t *testing.T) {
	ai := &fakeGenerator{}
	g := newAudioGenerator(ai)

	_, err := g.GenerateAudio(context.Background(), AudioRequest{})

	assert.ErrorIs(t, err, ErrNilInput)
	assert.Zero(t, ai.callCount(), "Lyria was called with an empty prompt")
}

// TestGenerateAudioDeduplicatesConcurrentCalls checks that concurrent calls with the same content
// collapse into one, while each caller still receives independent bytes.
func TestGenerateAudioDeduplicatesConcurrentCalls(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: audioResponse("audio/mpeg", []byte{1, 2, 3}, "sung lyrics")}
		g := newAudioGenerator(ai)

		seed := int64(7)
		images := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}

		const callers = 5
		results := make([]*Track, callers)
		errs := make([]error, callers)

		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				results[i], errs[i] = g.GenerateAudio(context.Background(), AudioRequest{Prompt: "full prompt", Seed: &seed, Images: images})
			})
		}

		synctest.Wait()
		close(release)
		wg.Wait()

		require.Equal(t, 1, ai.callCount(), "concurrent calls with the same content were not collapsed")
		for _, err := range errs {
			require.NoError(t, err)
		}

		// A copy is returned, not the shared result.
		require.NotSame(t, results[0], results[1])
		results[0].Audio[0] = 9
		assert.Equal(t, byte(1), results[1].Audio[0], "the audio bytes are shared")
		// The text returned with the audio reaches everyone.
		assert.Equal(t, "sung lyrics", results[1].SungLyrics)
	})
}

// TestGenerateAudioSeparatesDifferentImages checks that calls with different images do not
// share a call. Images change the result, so if they were not in the key, audio made with one
// image would be returned to the other.
func TestGenerateAudioSeparatesDifferentImages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		ai := &fakeGenerator{block: release, resp: audioResponse("audio/mpeg", []byte{1, 2, 3}, "sung lyrics")}
		g := newAudioGenerator(ai)

		seed := int64(7)
		imagesA := []ImagePayload{{Data: []byte("image-a"), MIMEType: "image/png"}}
		imagesB := []ImagePayload{{Data: []byte("image-b"), MIMEType: "image/png"}}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, images := range [][]ImagePayload{imagesA, imagesB} {
			wg.Go(func() {
				_, errs[i] = g.GenerateAudio(context.Background(), AudioRequest{Prompt: "full prompt", Seed: &seed, Images: images})
			})
		}

		// Wait until both are in flight. If they did not share a call, there are two calls.
		synctest.Wait()
		got := ai.callCount()

		close(release)
		wg.Wait()

		assert.Equal(t, 2, got, "calls with different images share one generation result")
		for _, err := range errs {
			require.NoError(t, err)
		}
	})
}

// TestImagesHashIgnoresEmptyPayloads checks that empty images do not affect the key.
// Callers build images as "pass it if there is one", so if the presence of empty elements
// changed the key, deduplication would stop working.
func TestImagesHashIgnoresEmptyPayloads(t *testing.T) {
	filled := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}
	withEmpty := []ImagePayload{
		{MIMEType: "image/png"},
		{Data: []byte("cover"), MIMEType: "image/png"},
	}

	assert.Equal(t, imagesHash(filled), imagesHash(withEmpty))
	assert.NotEqual(t, imagesHash(filled), imagesHash(nil))
	assert.Equal(t, imagesHash(nil), imagesHash([]ImagePayload{}))
}

// TestImagesHashDistinguishesContentAndType checks that the key changes when either the bytes
// or the MIME type differ.
func TestImagesHashDistinguishesContentAndType(t *testing.T) {
	base := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/png"}}
	otherData := []ImagePayload{{Data: []byte("other"), MIMEType: "image/png"}}
	otherType := []ImagePayload{{Data: []byte("cover"), MIMEType: "image/jpeg"}}

	assert.NotEqual(t, imagesHash(base), imagesHash(otherData))
	assert.NotEqual(t, imagesHash(base), imagesHash(otherType))
}
