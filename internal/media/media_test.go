package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
)

const testProfileVersion = "10000000-0000-4000-8000-000000000001"

func testProfile(t *testing.T) Profile {
	t.Helper()
	p, err := NewProfile(ProfileInput{Version: testProfileVersion, VariantKey: "default", Publication: PublicationUnpublished})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testInputRaw(t *testing.T, p Profile, size string, prompt string) []byte {
	t.Helper()
	raw, err := json.Marshal(inputWire{"1", testProfileVersion, "default", prompt, size, FormatPNG, "1"})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testInput(t *testing.T, p Profile) Input {
	t.Helper()
	in, err := ParseInput(testInputRaw(t, p, "256x256", "a bounded prompt"), p)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func testPNG(t *testing.T, size ImageSize) []byte {
	t.Helper()
	imageValue := image.NewRGBA(image.Rect(0, 0, size.Width, size.Height))
	for y := 0; y < size.Height; y++ {
		for x := 0; x < size.Width; x++ {
			imageValue.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 17, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageValue); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testResultRaw(t *testing.T, p Profile, in Input, data []byte, size ImageSize) []byte {
	t.Helper()
	digest := sha256.Sum256(data)
	wire := resultWire{
		Version:        "1",
		ProfileVersion: testProfileVersion,
		VariantKey:     "default",
		Format:         FormatPNG,
		DataBase64:     base64.StdEncoding.EncodeToString(data),
		Width:          "256",
		Height:         "256",
		ByteLength:     "0",
		SHA256:         hex.EncodeToString(digest[:]),
	}
	if size.Width == 512 {
		wire.Width = "512"
	}
	if size.Width == 1024 {
		wire.Width = "1024"
	}
	wire.Height = wire.Width
	wire.ByteLength = jsonNumberString(len(data))
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func jsonNumberString(value int) string {
	return strings.TrimSpace(strings.TrimPrefix(string(mustJSON(value)), "\""))
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestProfileIsUnpublishedAndImmutable(t *testing.T) {
	p := testProfile(t)
	if err := p.DispatchCheck(); !errors.Is(err, ErrProfileNotPublished) {
		t.Fatalf("dispatch unexpectedly allowed: %v", err)
	}
	if _, err := NewProfile(ProfileInput{Version: testProfileVersion, VariantKey: "default", Publication: PublicationPublished}); !errors.Is(err, ErrProfileNotPublished) {
		t.Fatalf("published input did not fail closed: %v", err)
	}
	for _, version := range []string{"00000000-0000-0000-0000-000000000000", "10000000-0000-4000-8000-00000000000A", "bad"} {
		if _, err := NewProfile(ProfileInput{Version: version, VariantKey: "default", Publication: PublicationUnpublished}); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("version %q was accepted: %v", version, err)
		}
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'x'
	again, err := p.CanonicalJSON()
	if err != nil || again[0] != '{' {
		t.Fatalf("canonical bytes were not copied: %v", err)
	}
	sizes, err := p.Sizes()
	if err != nil {
		t.Fatal(err)
	}
	sizes[0].Width = 1
	other, _ := p.Sizes()
	if other[0].Width != 256 {
		t.Fatal("profile sizes were not copied")
	}
}

func TestInputStrictBoundaries(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	if prompt, _ := in.Prompt(); prompt != "a bounded prompt" {
		t.Fatalf("unexpected prompt %q", prompt)
	}
	canonical, err := in.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonical[0] = 'x'
	again, _ := in.CanonicalJSON()
	if again[0] != '{' {
		t.Fatal("input canonical bytes were not copied")
	}
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"unknown field", []byte(`{"version":"1","profile_version":"` + testProfileVersion + `","variant_key":"default","prompt":"x","size":"256x256","format":"png","n":"1","extra":"x"}`), ErrInvalidInput},
		{"duplicate field", []byte(`{"version":"1","profile_version":"` + testProfileVersion + `","variant_key":"default","prompt":"x","size":"256x256","format":"png","n":"1","n":"1"}`), ErrInvalidJSON},
		{"trailing json", append(testInputRaw(t, p, "256x256", "x"), []byte(` {}`)...), ErrInvalidJSON},
		{"BOM", append([]byte{0xef, 0xbb, 0xbf}, testInputRaw(t, p, "256x256", "x")...), ErrInvalidJSON},
		{"numeric n", []byte(`{"version":"1","profile_version":"` + testProfileVersion + `","variant_key":"default","prompt":"x","size":"256x256","format":"png","n":1}`), ErrInvalidInput},
		{"control prompt", testInputRaw(t, p, "256x256", "a\x00b"), ErrInvalidInput},
		{"unknown size", testInputRaw(t, p, "2048x2048", "x"), ErrInvalidInput},
		{"wrong profile", []byte(`{"version":"1","profile_version":"20000000-0000-4000-8000-000000000002","variant_key":"default","prompt":"x","size":"256x256","format":"png","n":"1"}`), ErrBindingMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseInput(tc.raw, p); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	tooLong := testInputRaw(t, p, "256x256", strings.Repeat("p", MaxPromptBytes+1))
	if _, err := ParseInput(tooLong, p); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized prompt returned %v", err)
	}
	if _, err := ParseInput(bytes.Repeat([]byte{'x'}, MaxInputJSONBytes+1), p); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized input returned %v", err)
	}
}

func TestInlineResultValidatesPNGAndOwnsBytes(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	data := testPNG(t, ImageSize{Width: 256, Height: 256})
	result, err := ParseResult(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}), p, in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := result.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	got[0] ^= 0xff
	again, _ := result.Bytes()
	if bytes.Equal(got, again) {
		t.Fatal("result bytes were not copied")
	}
	for name, mutate := range map[string]func(*resultWire){
		"bad hash":   func(w *resultWire) { w.SHA256 = strings.Repeat("0", 64) },
		"bad length": func(w *resultWire) { w.ByteLength = "1" },
		"bad format": func(w *resultWire) { w.Format = "jpeg" },
	} {
		t.Run(name, func(t *testing.T) {
			wire := resultWire{
				Version: "1", ProfileVersion: testProfileVersion, VariantKey: "default", Format: FormatPNG,
				DataBase64: base64.StdEncoding.EncodeToString(data), Width: "256", Height: "256",
				ByteLength: jsonNumberString(len(data)), SHA256: hex.EncodeToString(sha256Bytes(data)),
			}
			mutate(&wire)
			raw, _ := json.Marshal(wire)
			if _, err := ParseResult(raw, p, in); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("got %v", err)
			}
		})
	}
	wrongSize := testPNG(t, ImageSize{Width: 512, Height: 512})
	if _, err := ParseResult(testResultRaw(t, p, in, wrongSize, ImageSize{Width: 512, Height: 512}), p, in); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("result/input size mismatch returned %v", err)
	}
	truncated := data[:len(data)/2]
	if _, err := ParseResult(testResultRaw(t, p, in, truncated, ImageSize{Width: 256, Height: 256}), p, in); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("truncated PNG returned %v", err)
	}
	for name, raw := range map[string][]byte{
		"duplicate": []byte(`{"version":"1","profile_version":"` + testProfileVersion + `","variant_key":"default","format":"png","data_base64":"x","width":"256","height":"256","byte_length":"1","sha256":"` + strings.Repeat("0", 64) + `","format":"png"}`),
		"unknown":   append(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256})[:len(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}))-1], []byte(`,"extra":"x"}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseResult(raw, p, in); !errors.Is(err, ErrInvalidJSON) && !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func sha256Bytes(data []byte) []byte {
	digest := sha256.Sum256(data)
	return digest[:]
}

func TestInlineResultResourceLimit(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	data := bytes.Repeat([]byte{0x7f}, MaxDecodedBytes+1)
	digest := sha256.Sum256(data)
	wire := resultWire{
		Version: "1", ProfileVersion: testProfileVersion, VariantKey: "default", Format: FormatPNG,
		DataBase64: base64.StdEncoding.EncodeToString(data), Width: "256", Height: "256",
		ByteLength: jsonNumberString(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseResult(raw, p, in); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("decoded resource over limit returned %v", err)
	}
}

func TestLifecycleNeverInfersConsumptionFromResult(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	data := testPNG(t, ImageSize{Width: 256, Height: 256})
	result, err := ParseResult(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}), p, in)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := NewLifecycle(p, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.DispatchCheck(); !errors.Is(err, ErrProfileNotPublished) {
		t.Fatalf("unpublished profile was dispatchable: %v", err)
	}
	if _, err := lifecycle.ConfirmUsage(UsageEvidence{}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("usage before result returned %v", err)
	}
	sending, err := lifecycle.ObserveDispatch()
	if err != nil {
		t.Fatal(err)
	}
	withResult, err := sending.ObserveResult(result)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := withResult.State()
	consumption, _ := withResult.Consumption()
	if state != StateResultReceived || consumption != ConsumptionUnknown {
		t.Fatalf("file result inferred consumption: state=%s consumption=%s", state, consumption)
	}
	if _, err := withResult.ConfirmUsage(UsageEvidence{}); !errors.Is(err, ErrUsageUnconfirmed) {
		t.Fatalf("missing usage returned %v", err)
	}
	stillResult, _ := withResult.State()
	if stillResult != StateResultReceived {
		t.Fatalf("failed confirmation mutated state to %s", stillResult)
	}
	evidence, err := NewUsageEvidence(UsageEvidenceInput{Source: "adapter", EventID: "usage-1"})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := withResult.ConfirmUsage(evidence)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = completed.State()
	consumption, _ = completed.Consumption()
	if state != StateCompleted || consumption != ConsumptionConfirmed {
		t.Fatalf("explicit usage did not complete: state=%s consumption=%s", state, consumption)
	}
	if _, err := completed.Cancel(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("terminal state was reopened: %v", err)
	}
}

func TestLifecyclePostDispatchFailureIsUnknown(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	lifecycle, _ := NewLifecycle(p, in)
	failedBefore, err := lifecycle.Fail(FailureResourceLimit)
	if err != nil {
		t.Fatal(err)
	}
	consumption, _ := failedBefore.Consumption()
	if consumption != ConsumptionNoDispatch {
		t.Fatalf("pre-dispatch failure was not no_dispatch: %s", consumption)
	}
	sending, _ := lifecycle.ObserveDispatch()
	failedAfter, err := sending.Fail(FailureInvalidResult)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := failedAfter.State()
	consumption, _ = failedAfter.Consumption()
	if state != StateFailed || consumption != ConsumptionUnknown {
		t.Fatalf("bad dispatched result was treated as no consumption: state=%s consumption=%s", state, consumption)
	}
	if _, err := failedAfter.ObserveDispatch(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("failed lifecycle reopened: %v", err)
	}
	cancelled, err := lifecycle.Cancel()
	if err != nil {
		t.Fatal(err)
	}
	consumption, _ = cancelled.Consumption()
	if consumption != ConsumptionNoDispatch {
		t.Fatalf("pre-dispatch cancellation was not no_dispatch: %s", consumption)
	}
	sending, _ = lifecycle.ObserveDispatch()
	cancelled, err = sending.Cancel()
	if err != nil {
		t.Fatal(err)
	}
	consumption, _ = cancelled.Consumption()
	if consumption != ConsumptionUnknown {
		t.Fatalf("post-dispatch cancellation was treated as no consumption: %s", consumption)
	}
	if _, err := NewUsageEvidence(UsageEvidenceInput{Source: "provider", EventID: "usage-1"}); !errors.Is(err, ErrInvalidUsage) {
		t.Fatalf("unapproved usage source accepted: %v", err)
	}
}

func TestImmutableReadersUnderConcurrency(t *testing.T) {
	p := testProfile(t)
	in := testInput(t, p)
	data := testPNG(t, ImageSize{Width: 256, Height: 256})
	result, err := ParseResult(testResultRaw(t, p, in, data, ImageSize{Width: 256, Height: 256}), p, in)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < 100; j++ {
				copyValue, _ := result.Bytes()
				copyValue[0] ^= byte(j)
				canonical, _ := result.CanonicalJSON()
				canonical[0] ^= byte(j)
				_, _ = result.Digest()
			}
		}()
	}
	wait.Wait()
}
