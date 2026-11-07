package media

import (
	"fmt"
)

var fixedSizes = [...]ImageSize{{Width: 256, Height: 256}, {Width: 512, Height: 512}, {Width: 1024, Height: 1024}}

type profileWire struct {
	Version            string        `json:"version"`
	ProfileVersion     string        `json:"profile_version"`
	Operation          Operation     `json:"operation"`
	ExecutionKind      ExecutionKind `json:"execution_kind"`
	ResultKind         ResultKind    `json:"result_kind"`
	VariantKey         string        `json:"variant_key"`
	Publication        Publication   `json:"publication"`
	InputFormat        string        `json:"input_format"`
	ResultFormat       Format        `json:"result_format"`
	MaxPromptBytes     string        `json:"max_prompt_bytes"`
	MaxOutputs         string        `json:"max_outputs"`
	AllowedSizes       []string      `json:"allowed_sizes"`
	MaxResultJSONBytes string        `json:"max_result_json_bytes"`
	MaxDecodedBytes    string        `json:"max_decoded_bytes"`
}

func NewProfile(in ProfileInput) (Profile, error) {
	if !validUUID(in.Version) || in.VariantKey != "default" {
		return Profile{}, ErrInvalidProfile
	}
	if in.Publication == PublicationPublished {
		return Profile{}, ErrProfileNotPublished
	}
	if in.Publication != PublicationUnpublished {
		return Profile{}, ErrInvalidProfile
	}
	wire := profileWire{
		Version:            "1",
		ProfileVersion:     in.Version,
		Operation:          OperationImageGeneration,
		ExecutionKind:      ExecutionSynchronous,
		ResultKind:         ResultInline,
		VariantKey:         "default",
		Publication:        PublicationUnpublished,
		InputFormat:        "json",
		ResultFormat:       FormatPNG,
		MaxPromptBytes:     fmt.Sprintf("%d", MaxPromptBytes),
		MaxOutputs:         "1",
		AllowedSizes:       []string{"256x256", "512x512", "1024x1024"},
		MaxResultJSONBytes: fmt.Sprintf("%d", MaxResultJSONBytes),
		MaxDecodedBytes:    fmt.Sprintf("%d", MaxDecodedBytes),
	}
	raw, ok := marshalCanonical(wire)
	if !ok {
		return Profile{}, ErrInvalidProfile
	}
	return Profile{
		valid:       true,
		version:     in.Version,
		variant:     "default",
		publication: PublicationUnpublished,
		canonical:   raw,
		digest:      frameDigest([]byte("moni-aigate.media.profile.v1"), raw),
	}, nil
}

func (p Profile) Version() (string, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return p.version, nil
}

func (p Profile) VariantKey() (string, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return p.variant, nil
}

func (p Profile) Publication() (Publication, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return p.publication, nil
}

func (p Profile) Operation() (Operation, error) {
	if !p.valid {
		return "", ErrInvalidValue
	}
	return OperationImageGeneration, nil
}

func (p Profile) Sizes() ([]ImageSize, error) {
	if !p.valid {
		return nil, ErrInvalidValue
	}
	return append([]ImageSize(nil), fixedSizes[:]...), nil
}

func (p Profile) SupportsSize(size ImageSize) bool {
	if !p.valid {
		return false
	}
	return validSize(size)
}

// DispatchCheck is deliberately closed for this first slice. A future
// provider-specific publication contract must replace this method's result;
// this package does not infer publication from a valid local fixture.
func (p Profile) DispatchCheck() error {
	if !p.valid {
		return ErrInvalidValue
	}
	return ErrProfileNotPublished
}

func (p Profile) CanonicalJSON() ([]byte, error) { return copyBytes(p.valid, p.canonical) }

func (p Profile) Digest() ([32]byte, error) {
	if !p.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return p.digest, nil
}

func validSize(size ImageSize) bool {
	for _, allowed := range fixedSizes {
		if allowed == size {
			return true
		}
	}
	return false
}

func sizeName(size ImageSize) string {
	return fmt.Sprintf("%dx%d", size.Width, size.Height)
}

func parseSize(value string) (ImageSize, bool) {
	for _, size := range fixedSizes {
		if sizeName(size) == value {
			return size, true
		}
	}
	return ImageSize{}, false
}
