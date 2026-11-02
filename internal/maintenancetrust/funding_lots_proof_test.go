package maintenancetrust

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/migrations"
)

// All fixtures below describe untrusted format rows. They do not manufacture
// maintenance authority or substitute for the real phase PostgreSQL tests.
func fundingLotsProofFormatFixture(t *testing.T, sandbox bool) fundinglotproof.V2Input {
	t.Helper()
	var input fundinglotproof.V2Input
	if sandbox {
		raw, predecessor := predecessorGolden(t)
		input = proofV2CompatInput(t, predecessor, raw)
	} else {
		pins := purePins()
		input = fundinglotproof.V2Input{Domain: fundinglotproof.DomainV2, ProofVersion: 2, Mode: "ordinary",
			EpochID:   "23456789-2345-4345-8345-23456789abcd",
			Database:  fundinglotproof.Database{Host: pins.Database.Host, Port: pins.Database.Port, Name: pins.Database.Name, OID: pins.Database.OID},
			AppSchema: fundingLotsProofSchema(pins.AppSchema), AppRole: fundingLotsProofRole(pins.App), ProjectorRole: fundingLotsProofRole(pins.Projector),
			BasisVersion: 1, CatalogDescriptorVersion: 2, CatalogPostgresMajor: 16,
			CatalogExpectedDigest: strings.Repeat("8", 64), CatalogSchemaDigest: strings.Repeat("9", 64), CatalogRolesDigest: strings.Repeat("a", 64)}
	}
	manifest, _, err := authorityManifestCanonical(migrations.Files)
	if err != nil || json.Unmarshal(manifest, &input.MigrationManifest) != nil {
		t.Fatal("fixed manifest fixture")
	}
	input.ManifestDigest = authorityManifestSHA
	input.BasisDigest = strings.Repeat("6", 64)
	input.BasisDescriptorDigest = fundingLotsBasisDescriptor
	return input
}

func fundingLotsProofFormatRows(t *testing.T, input fundinglotproof.V2Input) (*fundinglotproof.CanonicalV2, fundinglotinit.Metadata, fundinglotverify.WalletLotsEpochRow, fundinglotverify.WalletLotsAttestationRow) {
	t.Helper()
	canonical, err := fundinglotproof.EncodeV2(input)
	if err != nil {
		t.Fatal("format fixture invalid")
	}
	at := fundinglotverify.WalletLotsTime{Value: time.Date(12000, 2, 3, 4, 5, 6, 123456000, time.UTC), Valid: true}
	metadata := fundinglotinit.Metadata{EpochID: input.EpochID, EpochAt: at,
		TenantCount: 7, ReceiptCount: 11, SettlementCount: 13, ApprovedRefundCount: 17, OpeningCount: 19, LegacySourceCount: 23,
		AvailableTotal: "18446744073709551614000", HeldTotal: "9223372036854775808000"}
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	e := fundinglotverify.WalletLotsEpochRow{Singleton: true, ID: input.EpochID, SchemaVersion: "1", DatabaseOID: u(uint64(input.Database.OID)), SchemaOID: u(uint64(input.AppSchema.OID)),
		BasisDigest: input.BasisDigest, ManifestDigest: input.ManifestDigest, DescriptorDigest: input.BasisDescriptorDigest, RuntimeProofDigest: canonical.SHA256(),
		TenantCount: "7", ReceiptCount: "11", SettlementCount: "13", ApprovedRefundCount: "17", OpeningCount: "19", LegacySourceCount: "23",
		AvailableTotal: metadata.AvailableTotal, HeldTotal: metadata.HeldTotal, Actor: "offline-maintenance", CreatedAt: at}
	a := fundinglotverify.WalletLotsAttestationRow{Singleton: true, EpochID: input.EpochID, ProofVersion: "2", Mode: input.Mode,
		DatabaseOID: e.DatabaseOID, SchemaOID: e.SchemaOID, SchemaOwnerOID: u(uint64(input.AppSchema.ObjectOwner.OID)),
		AppRoleOID: u(uint64(input.AppRole.OID)), ProjectorRoleOID: u(uint64(input.ProjectorRole.OID)),
		PredecessorPaymentDigest: cloneLeasePointer(input.PredecessorPaymentDigest), ProofCanonical: canonical.Bytes(), ProofSHA256: canonical.SHA256(),
		ManifestDigest: input.ManifestDigest, DescriptorDigest: input.BasisDescriptorDigest, CreatedAt: at}
	if input.Sandbox != nil {
		worker := u(uint64(input.Sandbox.WorkerRole.OID))
		a.WorkerRoleOID = &worker
	}
	return canonical, metadata, e, a
}

func fundingLotsProofCopyInput(t *testing.T, input fundinglotproof.V2Input) fundinglotproof.V2Input {
	t.Helper()
	canonical, err := fundinglotproof.EncodeV2(input)
	if err != nil {
		t.Fatal("copy fixture")
	}
	out, err := canonical.Bindings()
	if err != nil {
		t.Fatal("copy binding")
	}
	return out
}

func TestFundingLotsProofBindingsEveryFieldAndNestedLeaf(t *testing.T) {
	input := fundingLotsProofFormatFixture(t, true)
	if reflect.TypeOf(input).NumField() != 21 || !fundingLotsProofBindingsEqual(input, fundingLotsProofCopyInput(t, input)) {
		t.Fatal("v2 binding field contract")
	}
	// Every nested scalar is independently changed, including every migration,
	// schema owner, all roles/OIDs and both original predecessor strings.
	var visit func(reflect.Value, []int)
	visit = func(value reflect.Value, path []int) {
		switch value.Kind() {
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				visit(value.Field(i), append(append([]int(nil), path...), i))
			}
		case reflect.Pointer:
			if !value.IsNil() {
				visit(value.Elem(), append(append([]int(nil), path...), -1))
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				visit(value.Index(i), append(append([]int(nil), path...), -i-2))
			}
		default:
			changed := fundingLotsProofCopyInput(t, input)
			field := reflect.ValueOf(&changed).Elem()
			for _, index := range path {
				if index == -1 {
					field = field.Elem()
				} else if index < -1 {
					field = field.Index(-index - 2)
				} else {
					field = field.Field(index)
				}
			}
			switch field.Kind() {
			case reflect.String:
				field.SetString(field.String() + "x")
			case reflect.Uint16, reflect.Uint32:
				field.SetUint(field.Uint() + 1)
			default:
				t.Fatal("unreviewed binding leaf")
			}
			if fundingLotsProofBindingsEqual(input, changed) || fundingLotsProofBindingsEqual(changed, input) {
				t.Fatalf("binding leaf ignored: %v", path)
			}
		}
	}
	visit(reflect.ValueOf(input), nil)
	for _, mutate := range []func(*fundinglotproof.V2Input){
		func(v *fundinglotproof.V2Input) { v.Sandbox = nil },
		func(v *fundinglotproof.V2Input) { v.PredecessorPaymentDigest = nil },
		func(v *fundinglotproof.V2Input) { v.PredecessorPaymentCanonicalBase64URL = nil },
		func(v *fundinglotproof.V2Input) { v.MigrationManifest = v.MigrationManifest[:21] },
	} {
		changed := fundingLotsProofCopyInput(t, input)
		mutate(&changed)
		if fundingLotsProofBindingsEqual(input, changed) || fundingLotsProofBindingsEqual(changed, input) {
			t.Fatal("nullable union or manifest cardinality ignored")
		}
	}
}

func TestFundingLotsProofRowsEveryStoredColumn(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		input := fundingLotsProofFormatFixture(t, sandbox)
		canonical, metadata, epoch, attestation := fundingLotsProofFormatRows(t, input)
		match := func(e fundinglotverify.WalletLotsEpochRow, a fundinglotverify.WalletLotsAttestationRow) bool {
			return fundingLotsProofRowsMatch(e, a, canonical, input, metadata, metadata.EpochAt)
		}
		if !match(epoch, attestation) {
			t.Fatal("complete descriptive rows rejected")
		}
		for _, kind := range []string{"epoch", "attestation"} {
			var count int
			if kind == "epoch" {
				count = reflect.TypeOf(epoch).NumField()
			} else {
				count = reflect.TypeOf(attestation).NumField()
			}
			for i := 0; i < count; i++ {
				e, a := epoch, attestation
				var value reflect.Value
				if kind == "epoch" {
					value = reflect.ValueOf(&e).Elem()
				} else {
					value = reflect.ValueOf(&a).Elem()
				}
				field := value.Field(i)
				switch field.Kind() {
				case reflect.Bool:
					field.SetBool(!field.Bool())
				case reflect.String:
					field.SetString(field.String() + "0")
				case reflect.Slice:
					field.SetBytes(append(bytes.Clone(field.Bytes()), ' '))
				case reflect.Pointer:
					changed := "0"
					if !field.IsNil() {
						changed = field.Elem().String() + "0"
					}
					field.Set(reflect.ValueOf(&changed))
				case reflect.Struct:
					field.Set(reflect.ValueOf(fundinglotverify.WalletLotsTime{}))
				default:
					t.Fatal("unreviewed stored column")
				}
				if match(e, a) {
					t.Fatalf("%s column %s ignored", kind, value.Type().Field(i).Name)
				}
			}
		}
		if sandbox {
			attestation.WorkerRoleOID = nil
			if match(epoch, attestation) {
				t.Fatal("missing sandbox worker accepted")
			}
		}
	}
}

func TestFundingLotsProofRejectsValidButForeignCanonicalBindings(t *testing.T) {
	input := fundingLotsProofFormatFixture(t, false)
	for _, mutate := range []func(*fundinglotproof.V2Input){
		func(v *fundinglotproof.V2Input) { v.BasisDigest = strings.Repeat("7", 64) },
		func(v *fundinglotproof.V2Input) {
			v.CatalogSchemaDigest, v.CatalogRolesDigest = v.CatalogRolesDigest, v.CatalogSchemaDigest
		},
		func(v *fundinglotproof.V2Input) { v.Database.OID++ },
		func(v *fundinglotproof.V2Input) { v.AppSchema.NamespaceOwner.OID++; v.AppSchema.ObjectOwner.OID++ },
	} {
		changed := fundingLotsProofCopyInput(t, input)
		mutate(&changed)
		canonical, metadata, epoch, attestation := fundingLotsProofFormatRows(t, changed)
		// The bytes, all row digests and row identities agree with each other.
		// Only comparison to the owned expected bindings detects the wrong proof.
		if fundingLotsProofRowsMatch(epoch, attestation, canonical, input, metadata, metadata.EpochAt) {
			t.Fatal("self-consistent but foreign format proof accepted")
		}
	}
	canonical, metadata, epoch, attestation := fundingLotsProofFormatRows(t, input)
	for _, total := range []string{"018446744073709551614000", "18446744073709551614000.0", "18446744073709551614001"} {
		epoch.AvailableTotal = total
		if fundingLotsProofRowsMatch(epoch, attestation, canonical, input, metadata, metadata.EpochAt) {
			t.Fatal("noncanonical or altered NUMERIC total accepted")
		}
	}
}

func TestFundingLotsProofPreservesPublicNamespaceOwner(t *testing.T) {
	pins := purePins()
	pins.AppSchema.Name = "public"
	pins.AppSchema.NamespaceOwner = fundinglotcatalog.RolePin{Name: "pg_database_owner", OID: 6171}
	if !validPins(pins) {
		t.Fatal("public fixture")
	}
	converted := fundingLotsProofSchema(pins.AppSchema)
	if converted.NamespaceOwner != (fundinglotproof.Role{Name: "pg_database_owner", OID: 6171}) || converted.ObjectOwner != fundingLotsProofRole(pins.AppSchema.ObjectOwner) || converted.NamespaceOwner == converted.ObjectOwner {
		t.Fatal("raw namespace owner was replaced by effective object owner")
	}
}

func TestFundingLotsProofBasisRequiresReviewedCompleteReport(t *testing.T) {
	valid := fundinglotbasis.Report{Scope: "fixed_109_table_basis_v1", PhysicalEOFComplete: true, ScannedTables: 109, ScannedColumns: 976,
		SourceRows: 100000, SourceBytes: 64 << 20, BasisSHA256: strings.Repeat("1", 64), ManifestSHA256: authorityManifestSHA,
		DescriptorSHA256: fundingLotsBasisDescriptor, SendMetadataSHA256: fundingLotsBasisSend}
	if !fundingLotsProofBasisValid(valid) {
		t.Fatal("exact complete basis boundary rejected")
	}
	for i := 0; i < reflect.TypeOf(valid).NumField(); i++ {
		changed := valid
		field := reflect.ValueOf(&changed).Elem().Field(i)
		switch field.Kind() {
		case reflect.Bool:
			field.SetBool(false)
		case reflect.String:
			field.SetString(field.String() + "x")
		case reflect.Uint64:
			field.SetUint(field.Uint() + 1)
		default:
			t.Fatal("unreviewed basis report field")
		}
		if fundingLotsProofBasisValid(changed) {
			t.Fatalf("basis report qualification ignored: %s", reflect.TypeOf(valid).Field(i).Name)
		}
	}
}

func TestFundingLotsProofNativeTimeBoundaries(t *testing.T) {
	for _, value := range []time.Time{
		time.Date(-4712, 12, 1, 2, 3, 4, 567890000, time.UTC), time.Time{},
		time.Date(12000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(294276, 1, 1, 0, 0, 0, 0, time.UTC),
	} {
		codec := &pgtype.TimestamptzCodec{}
		native := pgtype.Timestamptz{Time: value, Valid: true}
		plan := codec.PlanEncode(nil, pgtype.TimestamptzOID, pgtype.BinaryFormatCode, native)
		if plan == nil {
			t.Fatal("native time encoder")
		}
		raw, err := plan.Encode(native, nil)
		if err != nil {
			t.Fatal("native time encoding")
		}
		decoded, err := fundingLotsProofTime(raw)
		if err != nil || !decoded.Value.Equal(value) || !fundingLotsProofTimeEqual(decoded, fundinglotverify.WalletLotsTime{Value: value.In(time.FixedZone("offset", 3600)), Valid: true}) {
			t.Fatal("finite native time envelope changed")
		}
	}
	for _, raw := range [][]byte{nil, {}, make([]byte, 7), make([]byte, 9)} {
		if got, err := fundingLotsProofTime(raw); err != ErrSchema || got != (fundinglotverify.WalletLotsTime{}) {
			t.Fatal("invalid native time result")
		}
	}
	for _, bits := range []uint64{1 << 63, 1<<63 - 1} {
		raw := make([]byte, 8)
		binary.BigEndian.PutUint64(raw, bits)
		if got, err := fundingLotsProofTime(raw); err != ErrSchema || got != (fundinglotverify.WalletLotsTime{}) {
			t.Fatal("infinite native time accepted")
		}
	}
	for _, invalid := range []fundinglotverify.WalletLotsTime{
		{}, {Value: time.Now().Truncate(time.Microsecond), Valid: true, Infinity: 1},
		{Value: time.Date(-4713, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		{Value: time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		{Value: time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC), Valid: true},
	} {
		if fundingLotsProofTimeEqual(invalid, invalid) {
			t.Fatal("invalid envelope equal to itself")
		}
	}
}

func TestFundingLotsProofZeroPhaseHasNoIssuanceOrPostAuthority(t *testing.T) {
	for _, phase := range []*fundingLotsPhase{nil, {}} {
		if phase.issueFundingLotsProof(context.Background()) != ErrInput || phase.checkFundingLotsPostProof(context.Background()) != ErrInput {
			t.Fatal("zero phase gained authority")
		}
	}
}

func TestFundingLotsProofRejectsWrongCurrentPhaseStageBeforeSQL(t *testing.T) {
	for stage := fundingLotsStage(0); stage <= fundingLotsDone; stage++ {
		phase, _ := pureFundingLotsPhase(t)
		gs := phase.snapshot.gate
		gs.mu.Lock()
		phase.stage = stage
		gs.mu.Unlock()
		if stage != fundingLotsDraft && phase.issueFundingLotsProof(phase.call.ctx) != ErrTransaction {
			t.Fatalf("issuer entered stage %d", stage)
		}
		if stage != fundingLotsPost && phase.checkFundingLotsPostProof(phase.call.ctx) != ErrTransaction {
			t.Fatalf("post verifier entered stage %d", stage)
		}
		if phase.canonical != nil || phase.expected != (FundingLotsExpected{}) || phase.pending != nil {
			t.Fatal("wrong-stage call issued data or entered SQL")
		}
	}
}
