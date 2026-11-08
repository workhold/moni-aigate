package provider

import "github.com/moni-ai/moni-aigate/internal/media"

// MediaAdapter is the provider-side name for the single media adapter
// contract. Provider implementations may satisfy this interface directly and
// are still required to return an explicit Sent/Handle decision.
type MediaAdapter = media.Adapter

type MediaSubmitInput = media.SubmitInput
type MediaSubmission = media.Submission
type MediaStream = media.Stream
type MediaStreamEvent = media.StreamEvent
