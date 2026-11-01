package billing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/pricing"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func (s *Store) Reserve(ctx context.Context, in ReserveInput) (Reservation, error) {
	var result Reservation
	if in.RequestID == "" {
		in.RequestID = uuid.NewString()
	}
	if !validID(in.RequestID) || !validID(in.Principal.TenantID) || !validID(in.Principal.UserID) || !validID(in.Principal.KeyID) || !validID(in.ModelID) || !boundedString(in.RequestHMAC, 256) || len(in.IdempotencyKey) > 200 {
		return result, fail("invalid_request", "invalid reservation input")
	}
	clientIP := security.NormalizeClientIP(in.Principal.ClientIP)
	if in.Principal.ClientIP != "" && clientIP == "" {
		return result, fail("invalid_request", "invalid request source")
	}
	in.Principal.ClientIP = clientIP
	if in.PolicyVersion == "" {
		in.PolicyVersion = "r3"
	}
	if _, exists := s.active.LoadOrStore(in.RequestID, true); exists {
		return result, &Error{Code: "request_exists", Message: "request already active", ResourceID: in.RequestID}
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if in.IdempotencyKey != "" {
			var inserted string
			err := tx.QueryRow(ctx, "INSERT INTO request_idempotency(tenant_id,key,request_hmac,request_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING request_id", in.Principal.TenantID, in.IdempotencyKey, in.RequestHMAC, in.RequestID).Scan(&inserted)
			if errors.Is(err, pgx.ErrNoRows) {
				var digest, original string
				if err = tx.QueryRow(ctx, "SELECT request_hmac,request_id FROM request_idempotency WHERE tenant_id=$1 AND operation='inference' AND key=$2", in.Principal.TenantID, in.IdempotencyKey).Scan(&digest, &original); err != nil {
					return err
				}
				code := "request_exists"
				if digest != in.RequestHMAC {
					code = "idempotency_conflict"
				}
				return &Error{Code: code, Message: "inference idempotency key already used", ResourceID: original}
			}
			if err != nil {
				return err
			}
		}
		auth, err := lockPrincipal(ctx, tx, in.Principal, in.ModelID, true)
		if err != nil {
			return err
		}
		if in.RouteRevision < 0 || in.RouteRevision > 0 && in.RouteRevision != auth.RouteRevision {
			return fail("route_changed", "route revision changed before reservation")
		}
		if in.ProfileVersion != "" && in.ProfileVersion != auth.ProfileVersion {
			return fail("profile_changed", "model profile changed before reservation")
		}
		var profile struct {
			Protocol      string `json:"protocol"`
			Input         int64  `json:"input_token_upper_bound"`
			Context       int64  `json:"context_limit"`
			Output        int64  `json:"output_limit"`
			DefaultOutput int64  `json:"default_output_limit"`
			OutputField   string `json:"output_limit_field"`
			Tools         bool   `json:"supports_tools"`
			ParallelTools bool   `json:"supports_parallel_tool_calls"`
		}
		if err = json.Unmarshal(auth.Profile, &profile); err != nil {
			return fail("invalid_profile", "invalid model profile")
		}
		// Input-only billing is limited to explicit retrieval protocols. A zero
		// output limit can never disable the generation reservation boundary.
		inputOnly := profile.Protocol == "openai_embeddings" || profile.Protocol == "jina_rerank"
		validOutput := profile.Output > 0 && in.OutputLimit > 0 && in.OutputLimit <= profile.Output
		if inputOnly {
			validOutput = profile.Output == 0 && profile.DefaultOutput == 0 && profile.OutputField == "" && !profile.Tools && !profile.ParallelTools && in.OutputLimit == 0
		}
		if profile.Input <= 0 || profile.Context <= 0 || in.InputUpperBound != profile.Input || !validOutput {
			return fail("invalid_bounds", "request does not satisfy published token bounds")
		}
		// Independent maxima need not be attainable simultaneously. Reserving
		// their sum is conservative; subtracting output from a shared context
		// is not a provider-enforced input bound.
		if profile.Input > profile.Context || profile.Output > profile.Context {
			return fail("invalid_profile", "an independent token bound exceeds context limit")
		}
		domain := pricing.Domain{GroupID: auth.PriceGroupID, TenantRevision: auth.PricingRevision}
		if err = pricing.LockAssignedGroupTx(ctx, tx, domain); err != nil {
			return err
		}
		selected, now, err := pricing.SelectEffectiveTx(ctx, tx, in.ModelID, domain)
		if err != nil {
			return err
		}
		if selected == nil {
			return fail("price_unavailable", "no effective model price")
		}
		price, inputRate, outputRate := selected.VersionID, selected.InputRate, selected.OutputRate
		pricedAt := now
		if inputOnly && outputRate != 0 {
			return fail("invalid_price", "input-only models require a zero output rate")
		}
		if !in.RequestDeadline.After(now) || in.RequestDeadline.After(now.Add(301*time.Second)) {
			return fail("request_deadline", "request deadline is invalid or expired")
		}
		amount, err := Calculate(in.InputUpperBound, in.OutputLimit, Money(inputRate), Money(outputRate))
		if err != nil {
			return err
		}
		spentHeld := new(big.Int).Add(big.NewInt(auth.Spent), big.NewInt(auth.Held))
		spentHeld.Add(spentHeld, big.NewInt(int64(amount)))
		if !spentHeld.IsInt64() {
			return fail("amount_overflow", "key budget exceeds supported range")
		}
		if auth.Limit != nil && spentHeld.Cmp(big.NewInt(*auth.Limit)) > 0 {
			return fail("key_budget_exceeded", "key budget cannot cover reservation")
		}
		controls, err := periodicbudget.LockCustomerTx(ctx, tx, in.Principal.TenantID, in.Principal.KeyID)
		if err != nil {
			return periodicError(err)
		}
		accounts, err := lockAccounts(ctx, tx, in.Principal.TenantID, "AVAILABLE", "HELD")
		if err != nil {
			return err
		}
		result = Reservation{ID: uuid.NewString(), RequestID: in.RequestID, TenantID: in.Principal.TenantID, Amount: amount, Status: "reserved", Deadline: now.Add(24 * time.Hour), PriceVersionID: price, InputRate: Money(inputRate), OutputRate: Money(outputRate), PricingGroupID: auth.PriceGroupID, TenantPricingRevision: auth.PricingRevision, RetailPricedAt: pricedAt}
		create := &pgx.Batch{}
		create.Queue("INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,NULLIF($18,'')::inet,$19,$20,$21)", in.RequestID, in.Principal.TenantID, in.Principal.KeyID, in.Principal.UserID, in.ModelID, price, inputRate, outputRate, in.InputUpperBound, in.OutputLimit, auth.ProfileVersion, in.PolicyVersion, in.RequestHMAC, in.RequestDeadline, s.instanceID, in.Streaming, in.RouteRevision, clientIP, auth.PriceGroupID, auth.PricingRevision, pricedAt)
		create.Queue("INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,$4,$5)", result.ID, result.TenantID, result.RequestID, int64(amount), result.Deadline)
		if err = tx.SendBatch(ctx, create).Close(); err != nil {
			return err
		}
		admission, err := periodicbudget.AdmitCustomerTx(ctx, tx, controls, result.RequestID, periodicbudget.Amount{Decimal: strconv.FormatInt(int64(amount), 10)})
		if err != nil {
			return periodicError(err)
		}
		journal, err := postLocked(ctx, tx, result.TenantID, result.ID, "reserve", result.RequestID, in.Principal.UserID, map[string]int64{"AVAILABLE": -int64(amount), "HELD": int64(amount)}, accounts)
		if err != nil {
			return err
		}
		if err = periodicbudget.RecordCustomerReserveTx(ctx, tx, controls, admission, result.RequestID); err != nil {
			return periodicError(err)
		}
		// The source clock was generated after every control and wallet wait.
		// Expiry still rolls all financial and periodic facts back together.
		now = admission.At
		if auth.KeyExpiresAt != nil && !auth.KeyExpiresAt.After(now) {
			return fail("invalid_key", "key is not active")
		}
		if !in.RequestDeadline.After(now) {
			return fail("request_deadline", "request deadline expired")
		}
		auditPayload, err := json.Marshal(map[string]any{"amount": amount})
		if err != nil {
			return err
		}
		eventPayload, err := json.Marshal(map[string]any{"amount": amount, "request_id": result.RequestID})
		if err != nil {
			return err
		}
		finish := &pgx.Batch{}
		finish.Queue("UPDATE api_keys SET held=held+$3 WHERE tenant_id=$1 AND id=$2", result.TenantID, in.Principal.KeyID, int64(amount))
		finish.Queue("INSERT INTO recovery_jobs(request_id,tenant_id) VALUES($1,$2)", result.RequestID, result.TenantID)
		finish.Queue(insertAuditSQL, in.Principal.UserID, result.TenantID, "billing.reserve", journal, auditPayload, result.RequestID)
		finish.Queue(insertOutboxSQL, result.TenantID, result.ID, "reserved", eventPayload)
		return tx.SendBatch(ctx, finish).Close()
	})
	if err != nil {
		s.active.Delete(in.RequestID)
		return Reservation{}, err
	}
	return result, nil
}
