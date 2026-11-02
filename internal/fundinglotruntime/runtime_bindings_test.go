package fundinglotruntime

import (
	"bytes"
	"encoding/base64"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type composeBindingFixture struct {
	pins     fundinglotcatalog.Pins
	current  currentCatalog
	expected *fundinglotproof.CanonicalV2
	epoch    fundinglotscan.EpochProofRows
	payment  fundinglotscan.ApplicationPaymentProofRows
	local    *applicationSnapshot
}

// These are descriptive format fixtures for the pure binding layer. They do
// not stand in for complete scan qualification or actual APP/PG acceptance.
func newComposeBindingFixture(t *testing.T, variant int) composeBindingFixture {
	t.Helper()
	m, err := loadMaterials()
	if err != nil {
		t.Fatal(err)
	}
	r := m.Mappings[variant]
	f := composeBindingFixture{current: currentCatalog{profile: fundinglotcatalog.WalletReadV2, version: 1, tuple: r.Current}}
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Ordinary,
		Database: fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: 100},
		AppSchema: fundinglotcatalog.SchemaPin{Name: "fixture_app", OID: 101,
			NamespaceOwner: fundinglotcatalog.RolePin{Name: "fixture_owner", OID: 201}, ObjectOwner: fundinglotcatalog.RolePin{Name: "fixture_owner", OID: 201}},
		App: fundinglotcatalog.RolePin{Name: "fixture_app", OID: 202}, Projector: fundinglotcatalog.RolePin{Name: "fixture_projector", OID: 208}}
	if strings.HasPrefix(r.Variant, "public-") {
		f.pins.AppSchema.Name = "public"
		f.pins.AppSchema.NamespaceOwner = fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}
	}
	if r.Mode == "sandbox" {
		raw, err := os.ReadFile("../fundinglotproof/testdata/predecessor_v1.json")
		if err != nil {
			t.Fatal(err)
		}
		v1, err := fundinglotproof.DecodeV1(raw)
		if err != nil {
			t.Fatal(err)
		}
		b, err := v1.Bindings()
		if err != nil {
			t.Fatal(err)
		}
		role := func(p fundinglotproof.V1Role) fundinglotcatalog.RolePin {
			return fundinglotcatalog.RolePin{Name: p.Name, OID: p.OID}
		}
		schema := func(p fundinglotproof.V1Schema) fundinglotcatalog.SchemaPin {
			return fundinglotcatalog.SchemaPin{Name: p.Name, OID: p.OID, NamespaceOwner: role(p.Owner), ObjectOwner: role(p.Owner)}
		}
		worker := role(b.Worker)
		f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Sandbox,
			Database:  fundinglotcatalog.DatabasePin{Host: b.Database.Host, Port: b.Database.Port, Name: b.Database.Name, OID: b.Database.OID},
			AppSchema: schema(b.App), App: role(b.App.Runtime), Projector: role(b.Projector), Worker: &worker,
			Sandbox: &fundinglotcatalog.SandboxPins{Mock: schema(b.Mock), MockRuntime: role(b.Mock.Runtime), Receiver: schema(b.Receiver), ReceiverRuntime: role(b.Receiver.Runtime)}}
		f.local = &applicationSnapshot{proof: v1, bindings: b, appNonceDigest: b.AppNonceDigest}
		at := time.Date(2001, 2, 3, 4, 5, 6, 123456000, time.UTC)
		f.payment = fundinglotscan.ApplicationPaymentProofRows{
			Environment: &fundinglotverify.SandboxGraphEnvironmentRow{Singleton: true, ID: b.EnvironmentID, MerchantID: b.MerchantID, PayeeLabel: b.PayeeLabel,
				DatabaseOID: runtimeOID(b.Database.OID), SchemaOID: runtimeOID(b.App.OID), NonceDigest: b.AppNonceDigest,
				AppRole: b.App.Runtime.Name, WorkerRole: b.Worker.Name, ProjectorRole: b.Projector.Name, ProtocolVersion: "1", CreatedAt: at},
			Attestation: &fundinglotverify.SandboxGraphAttestationRow{Singleton: true, EnvironmentID: b.EnvironmentID,
				DatabaseOID: runtimeOID(b.Database.OID), SchemaOID: runtimeOID(b.App.OID), SchemaOwnerOID: runtimeOID(b.App.Owner.OID),
				AppRoleOID: runtimeOID(b.App.Runtime.OID), WorkerRoleOID: runtimeOID(b.Worker.OID), ProjectorRoleOID: runtimeOID(b.Projector.OID),
				AppNonceDigest: b.AppNonceDigest, WorkerNonceDigest: b.WorkerNonceDigest, ProofVersion: "1", ProofCanonical: raw, ProofSHA256: v1.SHA256(), CreatedAt: at},
		}
	}
	b := fundinglotproof.V2Input{
		Domain: fundinglotproof.DomainV2, ProofVersion: 2, Mode: r.Mode, EpochID: "23456789-2345-4345-8345-23456789abcd",
		Database:  fundinglotproof.Database{Host: f.pins.Database.Host, Port: f.pins.Database.Port, Name: f.pins.Database.Name, OID: f.pins.Database.OID},
		AppSchema: runtimeProofSchema(f.pins.AppSchema), AppRole: runtimeProofRole(f.pins.App), ProjectorRole: runtimeProofRole(f.pins.Projector),
		BasisVersion: 1, BasisDigest: strings.Repeat("a", 64), BasisDescriptorDigest: m.BasisDescriptor,
		MigrationManifest: append([]fundinglotproof.Migration(nil), m.Manifest...), ManifestDigest: m.ManifestDigest,
		CatalogDescriptorVersion: 1, CatalogPostgresMajor: 16,
		CatalogExpectedDigest: r.Historical.Expected, CatalogSchemaDigest: r.Historical.Schema, CatalogRolesDigest: r.Historical.Roles,
	}
	if f.local != nil {
		b.Sandbox = &fundinglotproof.Sandbox{MockSchema: runtimeProofSchema(f.pins.Sandbox.Mock), MockRole: runtimeProofRole(f.pins.Sandbox.MockRuntime),
			ReceiverSchema: runtimeProofSchema(f.pins.Sandbox.Receiver), ReceiverRole: runtimeProofRole(f.pins.Sandbox.ReceiverRuntime), WorkerRole: runtimeProofRole(*f.pins.Worker)}
		digest, original := f.local.proof.SHA256(), base64.RawURLEncoding.EncodeToString(f.local.proof.Bytes())
		b.PredecessorPaymentDigest, b.PredecessorPaymentCanonicalBase64URL = &digest, &original
	}
	f.expected, err = fundinglotproof.EncodeV2(b)
	if err != nil {
		t.Fatal(err)
	}
	at := fundinglotverify.WalletLotsTime{Valid: true, Value: time.Date(2001, 2, 3, 4, 5, 6, 123456000, time.UTC)}
	f.epoch = fundinglotscan.EpochProofRows{
		Epoch: fundinglotverify.WalletLotsEpochRow{Singleton: true, ID: b.EpochID, SchemaVersion: "1", DatabaseOID: runtimeOID(f.pins.Database.OID), SchemaOID: runtimeOID(f.pins.AppSchema.OID),
			BasisDigest: b.BasisDigest, ManifestDigest: b.ManifestDigest, DescriptorDigest: b.BasisDescriptorDigest, RuntimeProofDigest: f.expected.SHA256(), Actor: "offline-maintenance", CreatedAt: at},
		Attestation: fundinglotverify.WalletLotsAttestationRow{Singleton: true, EpochID: b.EpochID, ProofVersion: "2", Mode: b.Mode,
			DatabaseOID: runtimeOID(f.pins.Database.OID), SchemaOID: runtimeOID(f.pins.AppSchema.OID), SchemaOwnerOID: runtimeOID(f.pins.AppSchema.ObjectOwner.OID),
			AppRoleOID: runtimeOID(f.pins.App.OID), ProjectorRoleOID: runtimeOID(f.pins.Projector.OID), PredecessorPaymentDigest: b.PredecessorPaymentDigest,
			ProofCanonical: f.expected.Bytes(), ProofSHA256: f.expected.SHA256(), ManifestDigest: b.ManifestDigest, DescriptorDigest: b.BasisDescriptorDigest, CreatedAt: at},
	}
	if f.pins.Worker != nil {
		oid := runtimeOID(f.pins.Worker.OID)
		f.epoch.Attestation.WorkerRoleOID = &oid
	}
	return f
}

func (f composeBindingFixture) bind(t *testing.T) (runtimeBinding, error) {
	t.Helper()
	return bindRuntimeOriginals(historyTestWindow(t), f.pins, f.current, f.expected, f.epoch, f.payment, f.local)
}

func TestRuntimeBindingsSixHistoricalMappings(t *testing.T) {
	for variant := 0; variant < 6; variant++ {
		f := newComposeBindingFixture(t, variant)
		bound, err := f.bind(t)
		if err != nil || bound.epoch != f.epoch.Epoch.ID || bound.proofSHA != f.expected.SHA256() || bound.compatibility.Current != f.current.tuple {
			t.Fatal("reviewed complete original binding rejected")
		}
	}
}

func TestRuntimeBindingsEveryAuthenticatedEpochColumn(t *testing.T) {
	for _, part := range []string{"epoch", "attestation"} {
		f := newComposeBindingFixture(t, 5)
		var value reflect.Value
		if part == "epoch" {
			value = reflect.ValueOf(&f.epoch.Epoch).Elem()
		} else {
			value = reflect.ValueOf(&f.epoch.Attestation).Elem()
		}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			// Historical counts/totals belong to the complete wallet replay,
			// whose qualified facts are mandatory before this pure comparison.
			if strings.HasSuffix(field.Name, "Count") || field.Name == "AvailableTotal" || field.Name == "HeldTotal" {
				continue
			}
			t.Run(part+"/"+field.Name, func(t *testing.T) {
				f := newComposeBindingFixture(t, 5)
				var target reflect.Value
				if part == "epoch" {
					target = reflect.ValueOf(&f.epoch.Epoch).Elem().Field(i)
				} else {
					target = reflect.ValueOf(&f.epoch.Attestation).Elem().Field(i)
				}
				switch target.Kind() {
				case reflect.String:
					target.SetString("changed")
				case reflect.Bool:
					target.SetBool(false)
				case reflect.Pointer, reflect.Slice:
					target.SetZero()
				case reflect.Struct:
					target.Set(reflect.ValueOf(fundinglotverify.WalletLotsTime{}))
				default:
					t.Fatal("uncovered epoch column type")
				}
				if _, err := f.bind(t); err != ErrRuntime {
					t.Fatal("changed authenticated epoch column accepted")
				}
			})
		}
	}
}

func TestRuntimeBindingsEveryApplicationOriginalColumn(t *testing.T) {
	for _, part := range []string{"environment", "attestation"} {
		f := newComposeBindingFixture(t, 5)
		var value reflect.Value
		if part == "environment" {
			value = reflect.ValueOf(f.payment.Environment).Elem()
		} else {
			value = reflect.ValueOf(f.payment.Attestation).Elem()
		}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			t.Run(part+"/"+field.Name, func(t *testing.T) {
				f := newComposeBindingFixture(t, 5)
				var target reflect.Value
				if part == "environment" {
					target = reflect.ValueOf(f.payment.Environment).Elem().Field(i)
				} else {
					target = reflect.ValueOf(f.payment.Attestation).Elem().Field(i)
				}
				switch target.Kind() {
				case reflect.String:
					target.SetString("changed")
				case reflect.Bool:
					target.SetBool(false)
				case reflect.Slice:
					target.SetZero()
				case reflect.Struct:
					// Environment year1/zero is legitimate; non-microsecond
					// precision is invalid for both native timestamp fields.
					target.Set(reflect.ValueOf(time.Date(2001, 1, 1, 0, 0, 0, 1, time.UTC)))
				default:
					t.Fatal("uncovered application column type")
				}
				if _, err := f.bind(t); err != ErrRuntime {
					t.Fatal("changed application original column accepted")
				}
			})
		}
	}
}

func TestRuntimeBindingsTimestampAndModeBoundaries(t *testing.T) {
	f := newComposeBindingFixture(t, 5)
	f.payment.Environment.CreatedAt = time.Time{}
	if _, err := f.bind(t); err != nil {
		t.Fatal("valid native year1 environment time rejected")
	}
	f.payment.Attestation.CreatedAt = time.Time{}
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("zero legacy attestation time accepted")
	}
	for _, year := range []int{-4712, 1, 9999, 294276} {
		f := newComposeBindingFixture(t, 0)
		at := fundinglotverify.WalletLotsTime{Valid: true, Value: time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)}
		f.epoch.Epoch.CreatedAt, f.epoch.Attestation.CreatedAt = at, at
		if _, err := f.bind(t); err != nil {
			t.Fatal("finite native PG epoch timestamp range narrowed")
		}
	}
	f = newComposeBindingFixture(t, 0)
	f.payment.Environment = &fundinglotverify.SandboxGraphEnvironmentRow{}
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("ordinary mode accepted an APP original")
	}
	f = newComposeBindingFixture(t, 5)
	f.local = nil
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("sandbox accepted missing local holder")
	}
	f = newComposeBindingFixture(t, 5)
	f.local.appNonceDigest = strings.Repeat("f", 64)
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("unheld APP nonce accepted")
	}
	f = newComposeBindingFixture(t, 0)
	f.expected = nil
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("nil expected adopted DB original")
	}
	// A second valid original is still a mismatch: expected is exact bytes.
	f = newComposeBindingFixture(t, 5)
	b, _ := f.expected.Bindings()
	b.EpochID = "34567890-3456-4456-8456-34567890abcd"
	f.expected, _ = fundinglotproof.EncodeV2(b)
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("different valid expected original accepted")
	}
	// A valid alternative V1 with the same roles is not the loaded local one.
	f = newComposeBindingFixture(t, 5)
	v1 := f.local.bindings
	v1.PayeeLabel = "different valid payee"
	alternative, err := fundinglotproof.EncodeV1(v1)
	if err != nil || bytes.Equal(alternative.Bytes(), f.local.proof.Bytes()) {
		t.Fatal("alternative valid V1 fixture")
	}
	f.payment.Attestation.ProofCanonical, f.payment.Attestation.ProofSHA256 = alternative.Bytes(), alternative.SHA256()
	if _, err := f.bind(t); err != ErrRuntime {
		t.Fatal("different valid V1 original accepted")
	}
}
