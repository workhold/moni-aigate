package maintenancetrust

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// This fixture represents untrusted values for pure comparison. It does not
// construct a CatalogProof or authenticate a connection, history, or raw graph.
type observedProofFixture struct {
	rows     fundinglotscan.EpochProofRows
	pins     fundinglotcatalog.Pins
	current  fundingLotsObservedCatalog
	manifest []byte
	mode     fundinglotcatalog.Mode
	prior    []byte
	priorSHA string
	expected FundingLotsExpected
	input    fundinglotproof.V2Input
}

func observedProofFixtureRows(t *testing.T, sandbox bool) observedProofFixture {
	t.Helper()
	input := fundingLotsProofFormatFixture(t, sandbox)
	canonical, _, epoch, attestation := fundingLotsProofFormatRows(t, input)
	pins := purePins()
	if sandbox {
		pins, _ = predecessorRowsFixture(t)
	}
	manifest, _, err := authorityManifestCanonical(copyAuthorityOriginal22MigrationFiles(t))
	if err != nil {
		t.Fatal("fixed observer manifest")
	}
	f := observedProofFixture{rows: fundinglotscan.EpochProofRows{Epoch: epoch, Attestation: attestation}, pins: pins,
		current:  fundingLotsObservedCatalog{version: input.CatalogDescriptorVersion, expected: input.CatalogExpectedDigest, schema: input.CatalogSchemaDigest, roles: input.CatalogRolesDigest},
		manifest: manifest, mode: pins.Mode, input: input,
		expected: FundingLotsExpected{EpochID: epoch.ID, BasisDigest: epoch.BasisDigest, ManifestDigest: epoch.ManifestDigest, DescriptorDigest: epoch.DescriptorDigest, RuntimeProofDigest: canonical.SHA256()}}
	if sandbox {
		f.prior, err = base64.RawURLEncoding.DecodeString(*input.PredecessorPaymentCanonicalBase64URL)
		if err != nil {
			t.Fatal("observer v1 fixture")
		}
		f.priorSHA = *input.PredecessorPaymentDigest
	}
	return f
}

func (f observedProofFixture) check() (FundingLotsExpected, error) {
	return fundingLotsObservedProofAgainst(f.rows, f.pins, f.current, f.manifest, f.mode, f.prior, f.priorSHA)
}

func TestFundingLotsObservedProofPureOrdinarySandboxAndNativeTime(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		f := observedProofFixtureRows(t, sandbox)
		for _, at := range []time.Time{
			time.Date(12000, 2, 3, 4, 5, 6, 123456000, time.UTC),
			time.Date(-4000, 2, 3, 4, 5, 6, 123456000, time.UTC),
		} {
			f.rows.Epoch.CreatedAt.Value, f.rows.Attestation.CreatedAt.Value = at, at
			got, err := f.check()
			if err != nil || got != f.expected {
				t.Fatalf("valid stored ordinary/sandbox/native-time binding rejected: %+v %v", got, err)
			}
		}
		// A historical basis is opaque after cutover. A different valid saved
		// basis is accepted when the actual immutable originals agree on it.
		input := fundingLotsProofCopyInput(t, f.input)
		input.BasisDigest = strings.Repeat("7", 64)
		canonical, _, epoch, attestation := fundingLotsProofFormatRows(t, input)
		f.rows = fundinglotscan.EpochProofRows{Epoch: epoch, Attestation: attestation}
		f.expected.BasisDigest, f.expected.RuntimeProofDigest = input.BasisDigest, canonical.SHA256()
		if got, err := f.check(); err != nil || got != f.expected {
			t.Fatal("saved basis incorrectly compared with a present business digest", err)
		}
	}
}

func TestFundingLotsObservedProofPurePersistentFieldBoundaries(t *testing.T) {
	for _, table := range []string{"Epoch", "Attestation"} {
		prototype := observedProofFixtureRows(t, true)
		row := reflect.ValueOf(prototype.rows).FieldByName(table)
		for i := 0; i < row.NumField(); i++ {
			name := row.Type().Field(i).Name
			// Historical amounts/counts are independently checked against the
			// complete immutable graph by ReadWallet, not this proof comparator.
			if strings.HasSuffix(name, "Count") || name == "AvailableTotal" || name == "HeldTotal" {
				continue
			}
			t.Run(table+"/"+name, func(t *testing.T) {
				f := observedProofFixtureRows(t, true)
				field := reflect.ValueOf(&f.rows).Elem().FieldByName(table).FieldByName(name)
				switch field.Kind() {
				case reflect.Bool:
					field.SetBool(false)
				case reflect.String:
					field.SetString(field.String() + "x")
				case reflect.Pointer:
					field.Set(reflect.Zero(field.Type()))
				case reflect.Slice:
					field.SetBytes(append(append([]byte(nil), field.Bytes()...), ' '))
				case reflect.Struct:
					field.FieldByName("Valid").SetBool(false)
				default:
					t.Fatalf("uncovered stored proof field %s", name)
				}
				if got, err := f.check(); err != ErrSchema || got != (FundingLotsExpected{}) {
					t.Fatalf("altered stored original accepted: %+v %v", got, err)
				}
			})
		}
	}
}

func TestFundingLotsObservedProofPureCurrentEnvironmentAndOriginals(t *testing.T) {
	for name, mutate := range map[string]func(*observedProofFixture){
		"nil manifest":           func(f *observedProofFixture) { f.manifest = nil },
		"wrong manifest":         func(f *observedProofFixture) { f.manifest[0] = ' ' },
		"mode differs":           func(f *observedProofFixture) { f.mode = fundinglotcatalog.Ordinary },
		"unknown mode":           func(f *observedProofFixture) { f.mode = 0 },
		"wrong database":         func(f *observedProofFixture) { f.pins.Database.OID++ },
		"wrong app namespace":    func(f *observedProofFixture) { f.pins.AppSchema.NamespaceOwner.OID++ },
		"wrong app object owner": func(f *observedProofFixture) { f.pins.AppSchema.ObjectOwner.OID++ },
		"wrong app role":         func(f *observedProofFixture) { f.pins.App.OID++ },
		"wrong worker role":      func(f *observedProofFixture) { f.pins.Worker.OID++ },
		"wrong projector role":   func(f *observedProofFixture) { f.pins.Projector.OID++ },
		"wrong peer schema":      func(f *observedProofFixture) { f.pins.Sandbox.Mock.OID++ },
		"wrong peer role":        func(f *observedProofFixture) { f.pins.Sandbox.ReceiverRuntime.OID++ },
		"wrong catalog version":  func(f *observedProofFixture) { f.current.version++ },
		"zero catalog version":   func(f *observedProofFixture) { f.current.version = 0 },
		"wrong catalog expected": func(f *observedProofFixture) { f.current.expected = strings.Repeat("b", 64) },
		"wrong catalog schema":   func(f *observedProofFixture) { f.current.schema = strings.Repeat("c", 64) },
		"wrong catalog roles":    func(f *observedProofFixture) { f.current.roles = strings.Repeat("d", 64) },
		"missing actual v1":      func(f *observedProofFixture) { f.prior = nil },
		"wrong actual v1 bytes":  func(f *observedProofFixture) { f.prior[0] = ' ' },
		"wrong actual v1 digest": func(f *observedProofFixture) { f.priorSHA = strings.Repeat("e", 64) },
		"oversized actual v1":    func(f *observedProofFixture) { f.prior = make([]byte, 65537) },
		"oversized stored proof": func(f *observedProofFixture) {
			f.rows.Attestation.ProofCanonical = make([]byte, fundingLotsProofLimit+1)
		},
		"mismatched native time": func(f *observedProofFixture) {
			f.rows.Attestation.CreatedAt.Value = f.rows.Attestation.CreatedAt.Value.Add(time.Microsecond)
		},
		"submicrosecond timestamp": func(f *observedProofFixture) {
			f.rows.Epoch.CreatedAt.Value = f.rows.Epoch.CreatedAt.Value.Add(time.Nanosecond)
		},
		"infinite timestamp": func(f *observedProofFixture) { f.rows.Epoch.CreatedAt.Infinity = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			f := observedProofFixtureRows(t, true)
			mutate(&f)
			if got, err := f.check(); err != ErrSchema || got != (FundingLotsExpected{}) {
				t.Fatalf("unmatched current environment/original accepted: %+v %v", got, err)
			}
		})
	}
	for name, mutate := range map[string]func(*observedProofFixture){
		"unexpected v1 digest": func(f *observedProofFixture) { f.priorSHA = strings.Repeat("a", 64) },
		"unexpected v1 bytes":  func(f *observedProofFixture) { f.prior = []byte(`{}`) },
		"unexpected worker":    func(f *observedProofFixture) { worker := "123"; f.rows.Attestation.WorkerRoleOID = &worker },
	} {
		t.Run("ordinary/"+name, func(t *testing.T) {
			f := observedProofFixtureRows(t, false)
			mutate(&f)
			if got, err := f.check(); err != ErrSchema || got != (FundingLotsExpected{}) {
				t.Fatalf("ordinary accepted sandbox union: %+v %v", got, err)
			}
		})
	}
}

func TestFundingLotsObservedProofRejectsAbsentCatalogAuthority(t *testing.T) {
	f := observedProofFixtureRows(t, false)
	for _, catalog := range []*fundinglotcatalog.CatalogProof{nil, {}} {
		got, err := fundingLotsObservedProof(f.rows, f.pins, catalog, f.manifest, f.mode, f.prior, f.priorSHA)
		if err != ErrSchema || got != (FundingLotsExpected{}) {
			t.Fatalf("descriptive rows manufactured authenticated catalog: %+v %v", got, err)
		}
	}
}
