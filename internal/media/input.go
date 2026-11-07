package media

type inputWire struct {
	Version        string `json:"version"`
	ProfileVersion string `json:"profile_version"`
	VariantKey     string `json:"variant_key"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size"`
	Format         Format `json:"format"`
	N              string `json:"n"`
}

func ParseInput(raw []byte, profile Profile) (Input, error) {
	if !profile.valid {
		return Input{}, ErrInvalidValue
	}
	m, err := parseEnvelope(raw, MaxInputJSONBytes)
	if err != nil {
		return Input{}, err
	}
	if !exactObject(m, "version", "profile_version", "variant_key", "prompt", "size", "format", "n") {
		return Input{}, ErrInvalidInput
	}
	version, versionOK := stringValue(m["version"])
	profileVersion, profileOK := stringValue(m["profile_version"])
	variant, variantOK := stringValue(m["variant_key"])
	prompt, promptOK := stringValue(m["prompt"])
	sizeText, sizeOK := stringValue(m["size"])
	formatText, formatOK := stringValue(m["format"])
	n, nValue, nOK := decimalString(m["n"])
	if !versionOK || !profileOK || !variantOK || !promptOK || !sizeOK || !formatOK || !nOK || version != "1" || variant != "default" || formatText != string(FormatPNG) || nValue != 1 || !safePrompt(prompt) {
		return Input{}, ErrInvalidInput
	}
	size, ok := parseSize(sizeText)
	if !ok {
		return Input{}, ErrInvalidInput
	}
	wantVersion, _ := profile.Version()
	if profileVersion != wantVersion || variant != profile.variant {
		return Input{}, ErrBindingMismatch
	}
	wire := inputWire{"1", profileVersion, variant, prompt, sizeName(size), FormatPNG, n}
	canonical, ok := marshalCanonical(wire)
	if !ok {
		return Input{}, ErrInvalidInput
	}
	return Input{
		valid:         true,
		profileDigest: profile.digest,
		profile:       profileVersion,
		variant:       variant,
		prompt:        prompt,
		size:          size,
		format:        FormatPNG,
		canonical:     canonical,
		digest:        frameDigest([]byte("moni-aigate.media.input.v1"), profile.digest[:], canonical),
	}, nil
}

func (in Input) ProfileVersion() (string, error) {
	if !in.valid {
		return "", ErrInvalidValue
	}
	return in.profile, nil
}

func (in Input) Prompt() (string, error) {
	if !in.valid {
		return "", ErrInvalidValue
	}
	return in.prompt, nil
}

func (in Input) Size() (ImageSize, error) {
	if !in.valid {
		return ImageSize{}, ErrInvalidValue
	}
	return in.size, nil
}

func (in Input) Format() (Format, error) {
	if !in.valid {
		return "", ErrInvalidValue
	}
	return in.format, nil
}

func (in Input) CanonicalJSON() ([]byte, error) { return copyBytes(in.valid, in.canonical) }

func (in Input) Digest() ([32]byte, error) {
	if !in.valid {
		return [32]byte{}, ErrInvalidValue
	}
	return in.digest, nil
}
