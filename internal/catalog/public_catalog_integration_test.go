package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

type publicFixture struct {
	*credentialPoolFixture
	admin, customer, finance string
}

func publicDB(t *testing.T) *publicFixture {
	t.Helper()
	f := &publicFixture{credentialPoolFixture: credentialPoolDB(t, true), admin: uuid.NewString(), customer: uuid.NewString(), finance: uuid.NewString()}
	for _, user := range []struct{ id, role string }{{f.admin, "admin"}, {f.customer, "none"}, {f.finance, "finance_operator"}} {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, user.id, user.id+"@public.invalid", user.role); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func publicProfileJSON(t *testing.T, name, protocol string) []byte {
	t.Helper()
	p := provider.Profile{Version: uuid.NewString(), Model: name, Protocol: protocol, ContextLimit: 100, InputTokenUpperBound: 80, OutputLimit: 20, DefaultOutputLimit: 10, SupportsTools: true}
	if p.InputOnly() {
		p.OutputLimit, p.DefaultOutputLimit, p.SupportsTools = 0, 0, false
	}
	if protocol == provider.ProtocolGemini {
		p.SupportsTools = false
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (f *publicFixture) modelWith(t *testing.T, name, protocol string, visible bool) string {
	t.Helper()
	body := publicProfileJSON(t, name, protocol)
	var profile map[string]any
	if err := json.Unmarshal(body, &profile); err != nil {
		t.Fatal(err)
	}
	profile["secret_canary"] = "sk-DO-NOT-LEAK"
	profile["upstream_model"] = "private-upstream"
	profile["future_field"] = map[string]any{"key": "private"}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return f.modelWithProfile(t, name, body, visible)
}

func (f *publicFixture) modelWithProfile(t *testing.T, name string, body []byte, visible bool) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := f.app.Exec(f.ctx, `SELECT meter_insert_text_model($1,$2,$3::jsonb,($3::jsonb->>'version'))`, id, name, body); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE models SET public_visible=$2 WHERE id=$1`, id, visible); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *publicFixture) price(t *testing.T, model string, input, output int64, delta time.Duration) string {
	t.Helper()
	id := uuid.NewString()
	return meterquote.PublishDefaultPrice(t, f.ctx, f.app, meterquote.DefaultPrice{
		ID: id, ModelID: model, Actor: f.admin, InputRate: input, OutputRate: output,
		EffectiveAt: time.Now().UTC().Add(delta).Truncate(time.Microsecond),
	})
}

func publication(t *testing.T, f *publicFixture, id string) catalog.PublicationView {
	t.Helper()
	v, err := f.store.ModelPublication(f.ctx, f.admin, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPublicCatalogMigrationDefaultsAndIndependentRevision(t *testing.T) {
	f := credentialPoolDB(t, false)
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "015" {
			break
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.owner.Exec(f.ctx, string(body)); err != nil {
			t.Fatal(name, err)
		}
	}
	old := uuid.NewString()
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'old','{}','old')`, old); err != nil {
		t.Fatal(err)
	}
	body, _ := migrations.Files.ReadFile("015_public_catalog.sql")
	if _, err = f.owner.Exec(f.ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`SELECT public_visible,public_revision FROM models WHERE id=$1`,
		`UPDATE models SET public_revision=999,enabled=false,active_revision=8 WHERE id=$1 RETURNING public_visible,public_revision`,
		`UPDATE models SET public_revision=999 WHERE id=$1 RETURNING public_visible,public_revision`,
	} {
		var visible bool
		var revision int64
		if err = f.owner.QueryRow(f.ctx, statement, old).Scan(&visible, &revision); err != nil || visible || revision != 1 {
			t.Fatal("old model/default/no-op was not private revision 1", visible, revision, err)
		}
	}
	var revision int64
	if err = f.owner.QueryRow(f.ctx, `UPDATE models SET public_visible=true,public_revision=999 WHERE id=$1 RETURNING public_revision`, old).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("direct SQL forged public revision", revision, err)
	}
	if err = f.owner.QueryRow(f.ctx, `INSERT INTO models(public_name,profile,profile_version,public_revision) VALUES('new','{}','new',999) RETURNING public_revision`).Scan(&revision); err != nil || revision != 1 {
		t.Fatal("insert forged public revision", revision, err)
	}
}

func TestPublicCatalogProjectionProtocolsPricesAndSecrets(t *testing.T) {
	f := publicDB(t)
	empty, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{})
	if err != nil || empty.Data == nil || len(empty.Data) != 0 || empty.AsOf.IsZero() || empty.AsOf.Location() != time.UTC || empty.HasMore {
		t.Fatal("empty page lost its DB instant or exposed private model", empty, err)
	}
	for i, protocol := range []string{"", provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		name := fmt.Sprintf("model-%d", i)
		id := f.modelWith(t, name, protocol, true)
		var current string
		if i != 2 { // One valid model intentionally has no current price.
			output := int64(9007199254740993)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				output = 0
			}
			current = f.price(t, id, 0, output, -time.Hour)
		}
		f.price(t, id, 999, 0, time.Hour)
		v, at, e := f.store.PublicModel(f.ctx, id)
		if e != nil || at.IsZero() || v.ID != id || v.PublicName != name || !v.Capabilities.TextOnly {
			t.Fatal("safe projection/detail failed", v, at, e)
		}
		wantProtocol := protocol
		if wantProtocol == "" {
			wantProtocol = provider.ProtocolChat
		}
		inputOnly := protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank
		if v.Protocol != wantProtocol || v.Capabilities.InputOnly != inputOnly || v.Capabilities.Streaming == inputOnly || v.Capabilities.Tools != (!inputOnly && protocol != provider.ProtocolGemini) {
			t.Fatal("protocol capabilities were invented or lost", v)
		}
		if current == "" {
			if v.Pricing.Status != "unavailable" || v.Pricing.PriceVersionID != nil || v.Pricing.Rates != nil || v.Pricing.EffectiveAt != nil {
				t.Fatal("missing price was treated as free or partially populated", v)
			}
		} else {
			var wire struct {
				Rates []struct {
					Dimension string `json:"dimension"`
					Numerator string `json:"numerator"`
				} `json:"rates"`
			}
			if json.Unmarshal(v.Pricing.Rates, &wire) != nil || v.Pricing.Status != "available" || v.Pricing.PriceVersionID == nil || *v.Pricing.PriceVersionID != current || len(wire.Rates) != 2 || wire.Rates[0].Numerator != "0" || (inputOnly && (wire.Rates[1].Dimension != "text_output_token" || wire.Rates[1].Numerator != "0")) || (!inputOnly && wire.Rates[1].Numerator != "9007199254740993") {
				t.Fatal("future/zero/large price projection is wrong", v)
			}
		}
		encoded, err := json.Marshal(v)
		if err != nil || strings.Contains(string(encoded), "DO-NOT-LEAK") || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "profile") {
			t.Fatal("projection leaked internal material", string(encoded), err)
		}
		var object map[string]json.RawMessage
		_ = json.Unmarshal(encoded, &object)
		keys := []string{}
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, []string{"capabilities", "id", "limits", "pricing", "protocol", "public_name"}) {
			t.Fatal("unexpected anonymous DTO fields", keys)
		}
	}
	page, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{})
	if err != nil || len(page.Data) != 6 || page.HasMore {
		t.Fatal(page, err)
	}
	for _, id := range []string{f.model, uuid.NewString()} {
		if _, _, err := f.store.PublicModel(f.ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("private and absent detail must be indistinguishable", id, err)
		}
	}
	var facts int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records)+(SELECT count(*) FROM reservations)+(SELECT count(*) FROM journals)+(SELECT count(*) FROM upstream_attempts)`).Scan(&facts); err != nil || facts != 0 {
		t.Fatal("anonymous reads created financial/model facts", facts, err)
	}
}

func TestPublicCatalogPaginationLiteralPrefixAndCurrentVisibility(t *testing.T) {
	f := publicDB(t)
	for i := 0; i < 52; i++ {
		f.modelWith(t, fmt.Sprintf("item-%02d", i), provider.ProtocolChat, true)
	}
	page, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{Prefix: "item-"})
	if err != nil || len(page.Data) != 50 || !page.HasMore || page.Data[0].PublicName != "item-00" || page.Data[49].PublicName != "item-49" {
		t.Fatal("50+1 page boundary failed", page, err)
	}
	last := page.Data[49]
	filter := catalog.PublicCatalogFilter{Prefix: "item-", AfterName: last.PublicName, AfterID: last.ID}
	second, err := f.store.PublicModels(f.ctx, filter)
	if err != nil || len(second.Data) != 2 || second.HasMore || second.AsOf.Before(page.AsOf) {
		t.Fatal("second page invalid", second, err)
	}
	hidden := second.Data[0].ID
	if _, err = f.app.Exec(f.ctx, `UPDATE models SET public_visible=false WHERE id=$1`, hidden); err != nil {
		t.Fatal(err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE models SET enabled=false WHERE id=$1`, second.Data[1].ID); err != nil {
		t.Fatal(err)
	}
	f.modelWith(t, "item--before-cursor", provider.ProtocolChat, true)
	afterHide, err := f.store.PublicModels(f.ctx, filter)
	if err != nil || len(afterHide.Data) != 0 || afterHide.AsOf.IsZero() || afterHide.HasMore {
		t.Fatal("old cursor bypassed current visibility or moved backwards", afterHide, err)
	}
	if _, _, err = f.store.PublicModel(f.ctx, hidden); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("hidden detail still visible", err)
	}
	for _, name := range []string{"literal_%\\model", "literal-AAmodel", "zeta", "Alpha", "alpha", "模型"} {
		f.modelWith(t, name, provider.ProtocolChat, true)
	}
	literal, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{Prefix: "literal_%\\"})
	if err != nil || len(literal.Data) != 1 || literal.Data[0].PublicName != "literal_%\\model" {
		t.Fatal("LIKE metacharacters were not literal", literal, err)
	}
	ordered, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{})
	if err != nil || ordered.Data[0].PublicName != "Alpha" || ordered.Data[1].PublicName != "alpha" {
		t.Fatal("catalog does not follow C collation", ordered, err)
	}
	protocolPage, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{Protocol: provider.ProtocolRerank})
	if err != nil || len(protocolPage.Data) != 0 || protocolPage.AsOf.IsZero() {
		t.Fatal("exact protocol filter or empty instant failed", protocolPage, err)
	}
	for _, id := range []string{hidden, uuid.NewString()} {
		if _, _, err = f.store.PublicModel(f.ctx, id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("not-found detail leaked reason", err)
		}
	}
}

func TestPublicCatalogPublicationCASRoleRecheckAndAtomicAudit(t *testing.T) {
	f := publicDB(t)
	initial := publication(t, f, f.model)
	if initial.PublicVisible || initial.PublicRevision != "1" || !initial.Enabled {
		t.Fatal(initial)
	}
	for _, actor := range []string{f.customer, f.finance, uuid.NewString(), "not-an-id"} {
		if _, err := f.store.ModelPublication(f.ctx, actor, f.model); !errors.Is(err, catalog.ErrPublicCatalogForbidden) {
			t.Fatal("non-admin read publication", actor, err)
		}
		if _, err := f.store.SetModelPublication(f.ctx, actor, f.model, 1, true); !errors.Is(err, catalog.ErrPublicCatalogForbidden) {
			t.Fatal("non-admin set publication", actor, err)
		}
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE models SET enabled=false WHERE id=$1`, f.model); err != nil {
		t.Fatal(err)
	}
	const contenders = 2
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.SetModelPublication(f.ctx, f.admin, f.model, 1, true)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, catalog.ErrPublicCatalogConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("publication CAS did not serialize", wins, conflicts)
	}
	set := publication(t, f, f.model)
	if set.Enabled || !set.PublicVisible || set.PublicRevision != "2" {
		t.Fatal("publication overwrote enabled state", set)
	}
	if _, _, err := f.store.PublicModel(f.ctx, f.model); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("disabled prepublication leaked", err)
	}
	if _, err := f.store.SetModelPublication(f.ctx, f.admin, f.model, 2, true); err != nil {
		t.Fatal("same-value current CAS is not a no-op", err)
	}
	var audits int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='model.publication.update'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("duplicate or missing publication audit", audits, err)
	}
	if _, err := f.store.SetModelPublication(f.ctx, f.admin, f.model, 1, true); !errors.Is(err, catalog.ErrPublicCatalogConflict) {
		t.Fatal("old revision same value passed", err)
	}
	// Change the independent route revision through a real publication, not a
	// dangling pointer rejected by the current profile/route domain guard.
	f.publish(t, 0)
	if _, err := f.owner.Exec(f.ctx, `UPDATE models SET enabled=true WHERE id=$1`, f.model); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.PublicModel(f.ctx, f.model); err != nil || publication(t, f, f.model).PublicRevision != "2" {
		t.Fatal("reenabling lost publication or bumped public revision", err)
	}
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION public_test_fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='model.publication.update' THEN RAISE EXCEPTION 'fixture failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER public_test_fail_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION public_test_fail_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetModelPublication(f.ctx, f.admin, f.model, 2, false); err == nil {
		t.Fatal("audit failure was ignored")
	}
	if got := publication(t, f, f.model); !got.PublicVisible || got.PublicRevision != "2" {
		t.Fatal("audit failure left partial publication", got)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SetModelPublication(f.ctx, f.admin, f.model, 2, false); !errors.Is(err, catalog.ErrPublicCatalogForbidden) {
		t.Fatal("stale admin role accepted", err)
	}
}

func TestPublicCatalogBadProfilePriceAndBoundedPayload(t *testing.T) {
	f := publicDB(t)
	input := f.modelWith(t, "bad-input-price", provider.ProtocolEmbeddings, false)
	profiles := []struct {
		name string
		body []byte
	}{
		{name: "bad-profile", body: []byte(fmt.Sprintf(`{"version":%q,"model":"bad-profile","protocol":"openai_chat","input_token_upper_bound":80,"context_limit":0,"output_limit":20,"default_output_limit":10,"output_limit_field":"max_tokens"}`, uuid.NewString()))},
		{name: "oversized-profile", body: []byte(fmt.Sprintf(`{"version":%q,"model":"oversized-profile","protocol":"openai_chat","input_token_upper_bound":80,"context_limit":100,"output_limit":20,"default_output_limit":10,"output_limit_field":"max_tokens","padding":%q}`, uuid.NewString(), strings.Repeat("x", 50000)))},
	}
	for _, profile := range profiles {
		bad := f.modelWithProfile(t, profile.name, profile.body, false)
		var beforeRevision int64
		if err := f.owner.QueryRow(f.ctx, `SELECT public_revision FROM models WHERE id=$1`, bad).Scan(&beforeRevision); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.SetModelPublication(f.ctx, f.admin, bad, beforeRevision, true); !errors.Is(err, catalog.ErrPublicCatalogInvalid) {
			t.Fatal("invalid profile was publicly published", err)
		}
		if _, err := f.owner.Exec(f.ctx, `UPDATE models SET public_visible=true WHERE id=$1`, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PublicModels(f.ctx, catalog.PublicCatalogFilter{}); !errors.Is(err, catalog.ErrPublicCatalogUnavailable) {
			t.Fatal("invalid public profile was skipped or disclosed", err)
		}
		view := publication(t, f, bad)
		var expected int64
		if err := f.owner.QueryRow(f.ctx, `SELECT public_revision FROM models WHERE id=$1`, bad).Scan(&expected); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.SetModelPublication(f.ctx, f.admin, bad, expected, false); err != nil || !view.PublicVisible {
			t.Fatal("damaged profile prevented hiding", view, err)
		}
	}
	newPrice := uuid.NewString()
	_, err := f.app.Exec(f.ctx, `SELECT meter_insert_token_price($1,$2,1,1,clock_timestamp(),$3,NULL,$4,clock_timestamp())`, newPrice, input, f.admin, uuid.NewString())
	var priceError *pgconn.PgError
	if !errors.As(err, &priceError) || priceError.Code != "P0001" || !strings.Contains(priceError.Where, "meter_insert_token_price") || priceError.Message != "input-only price requires zero output rate" {
		t.Fatal("new malformed public price was not rejected by current meter publisher", err)
	}
	var legacyRows, attemptedRows int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM price_versions p WHERE p.model_id=$1 AND EXISTS (SELECT 1 FROM price_rate_lines l WHERE l.price_version_id=p.id AND l.dimension='text_output_token' AND l.numerator=1 AND l.denominator=1)),(SELECT count(*) FROM price_versions WHERE id=$2)`, input, newPrice).Scan(&legacyRows, &attemptedRows); err != nil || legacyRows != 0 || attemptedRows != 0 {
		t.Fatal("malformed price was accepted", legacyRows, attemptedRows, err)
	}
	if _, err = f.app.Exec(f.ctx, `UPDATE models SET public_visible=true WHERE id=$1`, input); err != nil {
		t.Fatal(err)
	}
	if view, _, err := f.store.PublicModel(f.ctx, input); err != nil || view.Pricing.Status != "unavailable" || view.Pricing.Rates != nil {
		t.Fatal("rejected price left a free or partially published quote", view, err)
	}
	// The projector gains no model/profile or publication write access.
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.projector}.Sanitize())
		return err
	}
	projector, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer projector.Close()
	_, err = projector.Exec(f.ctx, `UPDATE models SET public_visible=false WHERE id=$1`, input)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
		t.Fatal("projector gained public model writes", err)
	}
}

func TestPublicCatalogSnapshotDoesNotLockModelAndAdminWaitRechecksRole(t *testing.T) {
	f := publicDB(t)
	id := f.modelWith(t, "snapshot-model", provider.ProtocolChat, true)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `UPDATE models SET public_visible=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	if _, _, err = f.store.PublicModel(ctx, id); err != nil {
		t.Fatal("anonymous snapshot waited for model row lock or hid uncommitted update", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.PublicModel(f.ctx, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("snapshot after hiding commit still returned model", err)
	}
	roleTx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer roleTx.Rollback(f.ctx)
	if _, err = roleTx.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := f.store.SetModelPublication(f.ctx, f.admin, id, 2, true)
		done <- e
	}()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, int32(roleTx.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case e := <-done:
			t.Fatal("publication did not synchronize admin row", e)
		case <-deadline.C:
			t.Fatal("publication did not reach role lock")
		case <-ticker.C:
		}
	}
	if err = roleTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, catalog.ErrPublicCatalogForbidden) {
		t.Fatal("role revocation while waiting was ignored", err)
	}
	if _, _, err = f.store.PublicModel(f.ctx, id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("revoked admin published model", err)
	}
}
