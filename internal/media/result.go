package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image/png"
	"strconv"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

type resultWire struct {
	Version        string `json:"version"`
	ProfileVersion string `json:"profile_version"`
	VariantKey     string `json:"variant_key"`
	Format         Format `json:"format"`
	DataBase64     string `json:"data_base64"`
	Width          string `json:"width"`
	Height         string `json:"height"`
	ByteLength     string `json:"byte_length"`
	SHA256         string `json:"sha256"`
}

type resultDescriptor struct {
	Version        string `json:"version"`
	ProfileVersion string `json:"profile_version"`
	VariantKey     string `json:"variant_key"`
	Format         Format `json:"format"`
	Width          string `json:"width"`
	Height         string `json:"height"`
	ByteLength     string `json:"byte_length"`
	SHA256         string `json:"sha256"`
}

func ParseResult(raw []byte, profile Profile, input Input) (InlineResult, error) {
	if !profile.valid || !input.valid {
		return InlineResult{}, ErrInvalidValue
	}
	if input.profileDigest != profile.digest || input.profile != profile.version || input.variant != profile.variant {
		return InlineResult{}, ErrBindingMismatch
	}
	m, err := parseEnvelope(raw, MaxResultJSONBytes)
	if err != nil {
		return InlineResult{}, err
	}
	if !exactObject(m, "version", "profile_version", "variant_key", "format", "data_base64", "width", "height", "byte_length", "sha256") {
		return InlineResult{}, ErrInvalidResult
	}
	version, versionOK := stringValue(m["version"])
	profileVersion, profileOK := stringValue(m["profile_version"])
	variant, variantOK := stringValue(m["variant_key"])
	formatText, formatOK := stringValue(m["format"])
	encoded, encodedOK := stringValue(m["data_base64"])
	_, width, widthOK := decimalString(m["width"])
	_, height, heightOK := decimalString(m["height"])
	_, byteLength, byteLengthOK := decimalString(m["byte_length"])
	shaText, shaOK := stringValue(m["sha256"])
	if !versionOK || !profileOK || !variantOK || !formatOK || !encodedOK || !widthOK || !heightOK || !byteLengthOK || !shaOK || version != "1" || variant != profile.variant || formatText != string(FormatPNG) || encoded == "" || width == 0 || height == 0 || byteLength == 0 || !validHex64(shaText) {
		return InlineResult{}, ErrInvalidResult
	}
	if profileVersion != profile.version {
		return InlineResult{}, ErrBindingMismatch
	}
	wantSize, _ := input.Size()
	if width > uint64(^uint(0)>>1) || height > uint64(^uint(0)>>1) || (ImageSize{Width: int(width), Height: int(height)} != wantSize) {
		return InlineResult{}, ErrInvalidResult
	}
	if len(encoded) > MaxResultJSONBytes {
		return InlineResult{}, ErrTooLarge
	}
	if byteLength > MaxDecodedBytes {
		return InlineResult{}, ErrTooLarge
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(data) != encoded {
		return InlineResult{}, ErrInvalidResult
	}
	if len(data) > MaxDecodedBytes {
		return InlineResult{}, ErrTooLarge
	}
	if uint64(len(data)) != byteLength {
		return InlineResult{}, ErrInvalidResult
	}
	actualDigest := sha256.Sum256(data)
	if hex.EncodeToString(actualDigest[:]) != shaText {
		return InlineResult{}, ErrInvalidResult
	}
	if err := validatePNG(data, wantSize); err != nil {
		return InlineResult{}, err
	}
	descriptor := resultDescriptor{"1", profile.version, profile.variant, FormatPNG, strconv.FormatUint(width, 10), strconv.FormatUint(height, 10), strconv.FormatUint(byteLength, 10), shaText}
	canonical, ok := marshalCanonical(descriptor)
	if !ok {
		return InlineResult{}, ErrInvalidResult
	}
	return InlineResult{
		valid:         true,
		profileDigest: profile.digest,
		profile:       profile.version,
		variant:       profile.variant,
		format:        FormatPNG,
		size:          wantSize,
		data:          append([]byte(nil), data...),
		canonical:     canonical,
		digest:        frameDigest([]byte("moni-aigate.media.inline-result.v1"), profile.digest[:], canonical, data),
	}, nil
}

func validatePNG(data []byte, want ImageSize) error {
	if len(data) < len(pngSignature) || !bytes.Equal(data[:len(pngSignature)], pngSignature) {
		return ErrInvalidResult
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != want.Width || config.Height != want.Height || config.Width <= 0 || config.Height <= 0 {
		return ErrInvalidResult
	}
	if uint64(config.Width) > uint64(MaxDecodedBytes)/4/uint64(config.Height) {
		return ErrTooLarge
	}
	imageValue, err := png.Decode(bytes.NewReader(data))
	if err != nil || imageValue.Bounds().Dx() != want.Width || imageValue.Bounds().Dy() != want.Height {
		return ErrInvalidResult
	}
	return nil
}

func (r InlineResult) ProfileVersion() (string, error) {
	if !r.valid {
		return "", ErrInvalidValue
	}
	return r.profile, nil
}

func (r InlineResult) Format() (Format, error) {
	if !r.valid {
		return "", ErrInvalidValue
	}
	return r.format, nil
}

func (r InlineResult) Size() (ImageSize, error) {
	if !r.valid {
		return ImageSize{}, ErrInvalidValue
	}
	return r.size, nil
}

func (r InlineResult) Bytes() ([]byte, error) { return copyBytes(r.valid, r.data) }

func (r InlineResult) ByteLength() (int, error) {
	if !r.valid {
		return 0, ErrInvalidValue
	}
	return len(r.data), nil
}

func (r InlineResult) CanonicalJSON() ([]byte, error) { return copyBytes(r.valid, r.canonical) }

func (r InlineResult) Digest() ([32]byte, error) {
	if !r.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return r.digest, nil
}

func (r InlineResult) String() string {
	if !r.valid {
		return "<invalid media result>"
	}
	return fmt.Sprintf("%s %dx%d (%d bytes)", r.format, r.size.Width, r.size.Height, len(r.data))
}
