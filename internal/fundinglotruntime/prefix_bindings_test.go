package fundinglotruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotprefixcompat"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

func prefixBindingRecords(t *testing.T) []fundinglotprefixcompat.Record {
	t.Helper()
	raw, err := os.ReadFile("../fundinglotprefixcompat/expected/compatibility-v1.json")
	var material struct {
		Mappings []fundinglotprefixcompat.Record `json:"mappings"`
	}
	if err != nil || json.Unmarshal(raw, &material) != nil || len(material.Mappings) != 12 {
		t.Fatal("independently stored V3 mappings unavailable")
	}
	return material.Mappings
}

func prefixReencodeBindings(t *testing.T, f *composeBindingFixture, b fundinglotproof.V2Bindings) {
	t.Helper()
	canonical, err := fundinglotproof.EncodeV2(b)
	if err != nil {
		t.Fatal("complete prefix original format fixture", err)
	}
	f.expected = canonical
	f.epoch.Epoch.BasisDigest, f.epoch.Epoch.DescriptorDigest, f.epoch.Epoch.RuntimeProofDigest = b.BasisDigest, b.BasisDescriptorDigest, canonical.SHA256()
	f.epoch.Attestation.DescriptorDigest = b.BasisDescriptorDigest
	f.epoch.Attestation.ProofCanonical, f.epoch.Attestation.ProofSHA256 = canonical.Bytes(), canonical.SHA256()
}

func prefixBindingFixture(t *testing.T, r fundinglotprefixcompat.Record) composeBindingFixture {
	t.Helper()
	legacy, err := loadMaterials()
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, old := range legacy.Mappings {
		if old.Mode == r.Mode && old.Variant == r.Variant {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("prefix route has no reviewed original identity fixture")
	}
	f := newComposeBindingFixture(t, index)
	b, err := f.expected.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	b.BasisDescriptorDigest = r.BasisDescriptor
	b.CatalogExpectedDigest, b.CatalogSchemaDigest, b.CatalogRolesDigest = r.Historical.Expected, r.Historical.Schema, r.Historical.Roles
	f.current = currentCatalog{profile: fundinglotcatalog.PeriodicPrefixV3, version: 1, tuple: runtimeTuple(r.Current)}
	prefixReencodeBindings(t, &f, b)
	return f
}

func TestPrefixRuntimeBinderTwelveHistoricalAndFreshOriginals(t *testing.T) {
	for _, r := range prefixBindingRecords(t) {
		t.Run(r.Mode+"/"+r.Variant+"/"+r.HistoricalProfile, func(t *testing.T) {
			f := prefixBindingFixture(t, r)
			original := f.expected.Bytes()
			bound, err := f.bind(t)
			if err != nil || bound.epoch != f.epoch.Epoch.ID || bound.proofSHA != f.expected.SHA256() || bound.basisDescriptorSHA != r.BasisDescriptor || bound.compatibility.Mode != r.Mode || bound.compatibility.Variant != r.Variant || bound.compatibility.Historical != runtimeTuple(r.Historical) || bound.compatibility.Current != runtimeTuple(r.Current) {
				t.Fatal("exact historical/current binder route rejected", err)
			}
			if !bytes.Equal(original, f.expected.Bytes()) || !bytes.Equal(original, f.epoch.Attestation.ProofCanonical) {
				t.Fatal("binding rewrote the historical proof")
			}
			b, _ := f.expected.Bindings()
			b.BasisDigest = strings.Repeat("7", 64)
			prefixReencodeBindings(t, &f, b)
			if newer, err := f.bind(t); err != nil || newer.basisSHA != b.BasisDigest {
				t.Fatal("historical saved basis was replaced by a present-business digest")
			}
		})
	}
}

func TestPrefixRuntimeBinderRejectsCrossMappingsAndLegacyUpgrade(t *testing.T) {
	records := prefixBindingRecords(t)
	for _, r := range records {
		t.Run(r.Mode+"/"+r.Variant+"/"+r.HistoricalProfile, func(t *testing.T) {
			for _, other := range records {
				if other.Current == r.Current {
					continue
				}
				f := prefixBindingFixture(t, r)
				f.current.tuple = runtimeTuple(other.Current)
				if result, err := f.bind(t); err != ErrRuntime || result != (runtimeBinding{}) {
					t.Fatal("cross-mode/variant current tuple accepted")
				}
			}
			for _, profile := range []fundinglotcatalog.Profile{0, fundinglotcatalog.LegacyV1, fundinglotcatalog.WalletReadV2, 255} {
				f := prefixBindingFixture(t, r)
				f.current.profile = profile
				if result, err := f.bind(t); err != ErrRuntime || result != (runtimeBinding{}) {
					t.Fatal("legacy or unknown profile automatically accepted V3 material")
				}
			}
			for _, field := range []string{"descriptor", "expected", "schema", "roles", "catalog_version"} {
				f := prefixBindingFixture(t, r)
				b, _ := f.expected.Bindings()
				switch field {
				case "descriptor":
					b.BasisDescriptorDigest = fundinglotbasis.PeriodicPrefixDescriptorSHA256
					if b.BasisDescriptorDigest == r.BasisDescriptor {
						b.BasisDescriptorDigest = basisDescriptorSHA
					}
				case "expected":
					b.CatalogExpectedDigest = strings.Repeat("f", 64)
				case "schema":
					b.CatalogSchemaDigest = strings.Repeat("f", 64)
				case "roles":
					b.CatalogRolesDigest = strings.Repeat("f", 64)
				case "catalog_version":
					b.CatalogDescriptorVersion++
				}
				// All stored descriptor/hash copies agree on the altered valid
				// original; rejection must still follow the fixed mapping.
				prefixReencodeBindings(t, &f, b)
				if result, err := f.bind(t); err != ErrRuntime || result != (runtimeBinding{}) {
					t.Fatal("unreviewed descriptor/root/version accepted", field)
				}
			}
		})
	}
}

type prefixNoConnectionTx struct{ pgx.Tx }

func (*prefixNoConnectionTx) Conn() *pgx.Conn { return nil }

func TestPrefixRuntimePublicEntriesFailClosedWithoutOriginalAuthority(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		index := 0
		if sandbox {
			index = 5
		}
		for _, scenario := range []string{"nil_context", "canceled", "expired", "nil_tx", "typed_nil_tx", "nil_original", "zero_original", "no_actual_connection"} {
			t.Run(map[bool]string{false: "ordinary", true: "sandbox"}[sandbox]+"/"+scenario, func(t *testing.T) {
				f := prefixBindingFixture(t, prefixBindingRecords(t)[2*index])
				ctx := context.Background()
				var tx pgx.Tx = &prefixNoConnectionTx{}
				expected := f.expected
				switch scenario {
				case "nil_context":
					ctx = nil
				case "canceled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "expired":
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
					defer cancel()
				case "nil_tx":
					tx = nil
				case "typed_nil_tx":
					tx = (*prefixNoConnectionTx)(nil)
				case "nil_original":
					expected = nil
				case "zero_original":
					expected = &fundinglotproof.CanonicalV2{}
				}
				var result *RuntimeProof
				var err error
				if sandbox {
					local := &ApplicationState{state: &applicationState{proof: f.local.proof, appNonceDigest: f.local.appNonceDigest}}
					result, err = VerifySandboxPeriodicPrefixApplicationTx(ctx, tx, f.pins, expected, local)
				} else {
					result, err = VerifyOrdinaryPeriodicPrefixApplicationTx(ctx, tx, f.pins, expected)
				}
				if result != nil || err != ErrRuntime {
					t.Fatal("new public entry issued proof without actual authority")
				}
			})
		}
	}
	for _, proof := range []*RuntimeProof{nil, {}, {state: &runtimeProofState{catalog: &fundinglotcatalog.CatalogProof{}, finalCatalog: &fundinglotcatalog.CatalogProof{}}}} {
		if proof.VerifyTx(context.Background(), &prefixNoConnectionTx{}) != ErrRuntime {
			t.Fatal("zero/descriptive catalog manufactured RuntimeProof authority")
		}
	}
}
