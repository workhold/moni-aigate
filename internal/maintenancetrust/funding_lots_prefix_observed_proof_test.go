package maintenancetrust

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotprefixcompat"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// Static descriptive fixtures exercise both consumers of the independently
// authenticated compatibility leaf. They grant no catalog or transaction proof.
func prefixObservedRecords(t *testing.T) []fundinglotprefixcompat.Record {
	t.Helper()
	raw, err := os.ReadFile("../fundinglotprefixcompat/expected/compatibility-v1.json")
	var material struct {
		Mappings []fundinglotprefixcompat.Record `json:"mappings"`
	}
	if err != nil || json.Unmarshal(raw, &material) != nil || len(material.Mappings) != 12 {
		t.Fatal("fixed prefix mappings unavailable")
	}
	return material.Mappings
}

func prefixObservedReencode(t *testing.T, f *observedProofFixture) {
	t.Helper()
	canonical, _, epoch, attestation := fundingLotsProofFormatRows(t, f.input)
	f.rows = fundinglotscan.EpochProofRows{Epoch: epoch, Attestation: attestation}
	f.expected = FundingLotsExpected{EpochID: epoch.ID, BasisDigest: epoch.BasisDigest, ManifestDigest: epoch.ManifestDigest,
		DescriptorDigest: epoch.DescriptorDigest, RuntimeProofDigest: canonical.SHA256()}
}

func prefixObservedFixture(t *testing.T, r fundinglotprefixcompat.Record) observedProofFixture {
	t.Helper()
	f := observedProofFixtureRows(t, r.Mode == "sandbox")
	if strings.HasPrefix(r.Variant, "public-") {
		f.pins.AppSchema.Name = "public"
		f.pins.AppSchema.NamespaceOwner = fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}
	}
	f.input.AppSchema = fundingLotsProofSchema(f.pins.AppSchema)
	f.input.CatalogDescriptorVersion = 1
	f.input.BasisDescriptorDigest = r.BasisDescriptor
	f.input.CatalogExpectedDigest, f.input.CatalogSchemaDigest, f.input.CatalogRolesDigest = r.Historical.Expected, r.Historical.Schema, r.Historical.Roles
	f.current = fundingLotsObservedCatalog{version: 1, expected: r.Current.Expected, schema: r.Current.Schema, roles: r.Current.Roles}
	prefixObservedReencode(t, &f)
	return f
}

func prefixObservedCheck(f observedProofFixture, profile fundinglotcatalog.Profile) (FundingLotsExpected, error) {
	return fundingLotsObservedProofAgainstProfile(f.rows, f.pins, f.current, f.manifest, f.mode, f.prior, f.priorSHA, profile)
}

func TestFundingLotsPrefixObservedTwelveHistoricalAndFreshOriginals(t *testing.T) {
	for _, r := range prefixObservedRecords(t) {
		t.Run(r.Mode+"/"+r.Variant+"/"+r.HistoricalProfile, func(t *testing.T) {
			f := prefixObservedFixture(t, r)
			original := append([]byte(nil), f.rows.Attestation.ProofCanonical...)
			if result, err := prefixObservedCheck(f, fundinglotcatalog.PeriodicPrefixV3); err != nil || result != f.expected {
				t.Fatal("exact original/current compatibility rejected", err)
			}
			if !bytes.Equal(original, f.rows.Attestation.ProofCanonical) {
				t.Fatal("observer rewrote historical proof")
			}
			f.input.BasisDigest = strings.Repeat("7", 64)
			prefixObservedReencode(t, &f)
			if result, err := prefixObservedCheck(f, fundinglotcatalog.PeriodicPrefixV3); err != nil || result != f.expected {
				t.Fatal("saved original basis compared with current business contents", err)
			}
		})
	}
}

func TestFundingLotsPrefixObservedRejectsCrossMappingsAndLegacyUpgrade(t *testing.T) {
	records := prefixObservedRecords(t)
	for _, r := range records {
		t.Run(r.Mode+"/"+r.Variant+"/"+r.HistoricalProfile, func(t *testing.T) {
			for _, other := range records {
				if r.Current == other.Current {
					continue
				}
				f := prefixObservedFixture(t, r)
				f.current = fundingLotsObservedCatalog{version: 1, expected: other.Current.Expected, schema: other.Current.Schema, roles: other.Current.Roles}
				if result, err := prefixObservedCheck(f, fundinglotcatalog.PeriodicPrefixV3); result != (FundingLotsExpected{}) || err != ErrSchema {
					t.Fatal("cross-mode/variant current tuple accepted")
				}
			}
			for _, profile := range []fundinglotcatalog.Profile{0, fundinglotcatalog.LegacyV1, fundinglotcatalog.WalletReadV2, 255} {
				f := prefixObservedFixture(t, r)
				if result, err := prefixObservedCheck(f, profile); result != (FundingLotsExpected{}) || err != ErrSchema {
					t.Fatal("legacy or unknown path automatically upgraded to V3")
				}
				if result, err := f.check(); result != (FundingLotsExpected{}) || err != ErrSchema {
					t.Fatal("old comparison entry accepted V3 current evidence")
				}
			}
			for _, field := range []string{"descriptor", "expected", "schema", "roles", "version", "mode"} {
				f := prefixObservedFixture(t, r)
				switch field {
				case "descriptor":
					f.input.BasisDescriptorDigest = fundinglotbasis.PeriodicPrefixDescriptorSHA256
					if f.input.BasisDescriptorDigest == r.BasisDescriptor {
						f.input.BasisDescriptorDigest = fundingLotsBasisDescriptor
					}
				case "expected":
					f.input.CatalogExpectedDigest = strings.Repeat("f", 64)
				case "schema":
					f.input.CatalogSchemaDigest = strings.Repeat("f", 64)
				case "roles":
					f.input.CatalogRolesDigest = strings.Repeat("f", 64)
				case "version":
					f.input.CatalogDescriptorVersion++
				case "mode":
					// The outer original mode must still agree with the pinned
					// environment; do not create an invalid V2 mode union.
					if f.mode == fundinglotcatalog.Ordinary {
						f.mode = fundinglotcatalog.Sandbox
					} else {
						f.mode = fundinglotcatalog.Ordinary
					}
				}
				prefixObservedReencode(t, &f)
				if result, err := prefixObservedCheck(f, fundinglotcatalog.PeriodicPrefixV3); result != (FundingLotsExpected{}) || err != ErrSchema {
					t.Fatal("consistent but unreviewed original accepted", field)
				}
			}
		})
	}
}

func TestFundingLotsPrefixObservedDescriptiveMappingsGrantNoAuthority(t *testing.T) {
	for _, r := range prefixObservedRecords(t) {
		f := prefixObservedFixture(t, r)
		for _, catalog := range []*fundinglotcatalog.CatalogProof{nil, {}} {
			result, err := fundingLotsObservedProof(f.rows, f.pins, catalog, f.manifest, f.mode, f.prior, f.priorSHA)
			if result != (FundingLotsExpected{}) || err != ErrSchema {
				t.Fatal("fixed mapping manufactured authenticated catalog authority")
			}
		}
	}
}
