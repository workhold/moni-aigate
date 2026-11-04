package inference

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

func inputOnly(p Protocol) bool { return p == ProtocolEmbeddings || p == ProtocolRerank }
func validProtocol(p Protocol) bool {
	switch p {
	case ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolEmbeddings, ProtocolRerank, ProtocolGemini:
		return true
	}
	return false
}

func NewTextProfile(in TextProfileInput) (Profile, error) {
	id, err := in.Schema.ID()
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrInvalidProfile, err)
	}
	dimensions, _ := in.Schema.Dimensions()
	retail, _ := in.Schema.Mask(metering.Retail)
	supplier, _ := in.Schema.Mask(metering.Supplier)
	if !validUUID(in.Version) || !safeText(in.Model, 200, false) || in.VariantKey != "default" || !validProtocol(in.Protocol) || in.ContractDigest == [32]byte{} {
		return Profile{}, ErrInvalidProfile
	}
	if len(dimensions) != 2 || dimensions[0].Dimension != "text_input_token" || dimensions[1].Dimension != "text_output_token" || len(retail) != 2 || len(supplier) != 2 || retail[0] != dimensions[0].Dimension || retail[1] != dimensions[1].Dimension || supplier[0] != dimensions[0].Dimension || supplier[1] != dimensions[1].Dimension {
		return Profile{}, ErrInvalidProfile
	}
	if in.InputTokenUpperBound <= 0 || in.InputTokenUpperBound > dimensions[0].MaximumQuantity || in.OutputLimit < 0 || in.OutputLimit > dimensions[1].MaximumQuantity || inputOnly(in.Protocol) != (in.OutputLimit == 0) {
		return Profile{}, ErrInvalidProfile
	}
	sd, _ := in.Schema.Digest()
	kinds := []ResultKind{ResultJSON}
	if !inputOnly(in.Protocol) {
		kinds = append(kinds, ResultEvents)
	}
	wire := struct {
		Version        string       `json:"version"`
		ProfileVersion string       `json:"profile_version"`
		Model          string       `json:"model"`
		Protocol       Protocol     `json:"protocol"`
		ExecutionKind  string       `json:"execution_kind"`
		ResultKinds    []ResultKind `json:"result_kinds"`
		SchemaID       string       `json:"schema_id"`
		SchemaDigest   string       `json:"schema_digest"`
		VariantKey     string       `json:"variant_key"`
		ContractDigest string       `json:"contract_digest"`
		Input          string       `json:"input_token_upper_bound"`
		Output         string       `json:"output_limit"`
	}{"1", in.Version, in.Model, in.Protocol, "synchronous", kinds, id, digestHex(sd), in.VariantKey, digestHex(in.ContractDigest), strconv.FormatInt(in.InputTokenUpperBound, 10), strconv.FormatInt(in.OutputLimit, 10)}
	raw, err := json.Marshal(wire)
	if err != nil {
		return Profile{}, ErrInvalidProfile
	}
	if len(raw) > 16_384 {
		return Profile{}, ErrTooLarge
	}
	return Profile{valid: true, info: in, canonical: raw, digest: frameDigest("moni-aigate.inference.profile.v1", sd[:], raw)}, nil
}

func (p Profile) Info() (ProfileInfo, error) {
	if !p.valid {
		return ProfileInfo{}, ErrInvalidValue
	}
	info := ProfileInfo{TextProfileInput: p.info, ResultKinds: []ResultKind{ResultJSON}}
	if !inputOnly(p.info.Protocol) {
		info.ResultKinds = append(info.ResultKinds, ResultEvents)
	}
	return info, nil
}
func (p Profile) Schema() (metering.Schema, error) {
	if !p.valid {
		return metering.Schema{}, ErrInvalidValue
	}
	return p.info.Schema, nil
}
func (p Profile) CanonicalJSON() ([]byte, error) { return canonicalCopy(p.valid, p.canonical) }
func (p Profile) Digest() ([32]byte, error) {
	if !p.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return p.digest, nil
}
