package catalog

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const pricingTimeLayout = "2006-01-02T15:04:05.000000Z"

type PricingError struct {
	Code            string
	CommitUnknown   bool
	MissingModelIDs []string
}

func (e *PricingError) Error() string { return e.Code }
func pricingFail(code string) error   { return &PricingError{Code: code} }

type PricingActor struct{ UserID string }
type GroupCreateCommand struct{ OperationID, Label string }
type GroupUpdateCommand struct {
	OperationID, GroupID, Label, State string
	ExpectedRevision                   int64
}
type GroupPriceCommand struct {
	OperationID, GroupID, ModelID      string
	SchemaID, SchemaDigest, VariantKey string
	InputRate, OutputRate              int64
	EffectiveAt                        time.Time
}
type GroupPriceCompileCommand struct {
	OperationID, GroupID, Multiplier string
	ModelIDs                         []string
	EffectiveAt                      time.Time
	ExpectedBaseVersionIDs           map[string]string
}
type DefaultPriceCommand struct {
	OperationID, ModelID, SchemaID, SchemaDigest, VariantKey string
	InputRate, OutputRate                                    int64
	EffectiveAt                                              time.Time
}
type CostPriceCommand struct {
	OperationID, ChannelID, UpstreamModel, SchemaID, SchemaDigest, VariantKey, Basis string
	InputRate, OutputRate                                                            *int64
	EffectiveAt                                                                      time.Time
	ExpectedChannelRevision                                                          int64
}
type TenantPricingCommand struct {
	OperationID, TenantID string
	ExpectedRevision      int64
	GroupID               *string
}
type GroupView struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	State     string `json:"state"`
	Revision  string `json:"revision"`
	CreatedAt string `json:"created_at"`
}
type AssignmentView struct {
	TenantID        string  `json:"tenant_id"`
	PriceGroupID    *string `json:"price_group_id"`
	PricingRevision string  `json:"pricing_revision"`
}
type GroupPriceView struct {
	ID           string          `json:"id"`
	ModelID      string          `json:"model_id"`
	PriceGroupID string          `json:"price_group_id"`
	Currency     string          `json:"currency"`
	SchemaID     string          `json:"schema_id"`
	SchemaDigest string          `json:"schema_digest"`
	VariantKey   string          `json:"variant_key"`
	Rates        json.RawMessage `json:"rates"`
	EffectiveAt  string          `json:"effective_at"`
	CreatedAt    string          `json:"created_at"`
}
type GroupPriceCompileItem struct {
	ModelID            string  `json:"model_id"`
	BasePriceVersionID string  `json:"base_price_version_id"`
	BaseInputRate      string  `json:"base_input_rate"`
	BaseOutputRate     string  `json:"base_output_rate"`
	InputRate          string  `json:"input_rate"`
	OutputRate         string  `json:"output_rate"`
	PriceVersionID     *string `json:"price_version_id"`
}
type GroupPriceCompileView struct {
	GroupID     string                  `json:"group_id"`
	Multiplier  string                  `json:"multiplier"`
	EffectiveAt string                  `json:"effective_at"`
	Items       []GroupPriceCompileItem `json:"items"`
}
type DefaultPriceView struct {
	ID           string          `json:"id"`
	ModelID      string          `json:"model_id"`
	PriceGroupID *string         `json:"price_group_id"`
	SchemaID     string          `json:"schema_id"`
	SchemaDigest string          `json:"schema_digest"`
	VariantKey   string          `json:"variant_key"`
	Currency     string          `json:"currency"`
	Rates        json.RawMessage `json:"rates"`
	EffectiveAt  string          `json:"effective_at"`
	CreatedAt    string          `json:"created_at"`
}
type CostPriceView struct {
	ID                      string          `json:"id"`
	ChannelID               string          `json:"channel_id"`
	UpstreamModel           string          `json:"upstream_model"`
	SchemaID                string          `json:"schema_id"`
	SchemaDigest            string          `json:"schema_digest"`
	VariantKey              string          `json:"variant_key"`
	Currency                string          `json:"currency"`
	Basis                   string          `json:"basis"`
	Rates                   json.RawMessage `json:"rates"`
	EffectiveAt             string          `json:"effective_at"`
	CreatedAt               string          `json:"created_at"`
	ExpectedChannelRevision string          `json:"expected_channel_revision"`
	ChannelRevision         string          `json:"channel_revision"`
}
type RetailPriceView struct {
	ModelID        string          `json:"model_id"`
	Source         string          `json:"source"`
	Status         string          `json:"status"`
	PriceGroupID   *string         `json:"price_group_id"`
	PriceVersionID *string         `json:"price_version_id"`
	SchemaID       string          `json:"schema_id"`
	SchemaDigest   string          `json:"schema_digest"`
	VariantKey     string          `json:"variant_key"`
	Currency       string          `json:"currency"`
	Rates          json.RawMessage `json:"rates"`
	EffectiveAt    *string         `json:"effective_at"`
	AsOf           string          `json:"as_of"`
}
type PricingOperationView struct {
	OperationID      string  `json:"operation_id"`
	Kind             string  `json:"kind"`
	Scope            string  `json:"scope"`
	Actor            string  `json:"actor"`
	Target           string  `json:"target"`
	InputFingerprint string  `json:"input_fingerprint"`
	ResourceID       string  `json:"resource_id"`
	Revision         *string `json:"revision"`
	CreatedAt        string  `json:"created_at"`
}
type PricingOperationReceipt struct {
	Operation PricingOperationView `json:"operation"`
	Result    any                  `json:"result"`
}
type PricingOperationResult[T any] struct {
	Operation PricingOperationView
	Result    T
	Replayed  bool
}
type PricingPage[T any] struct {
	Data       []T
	NextCursor string
}
type PricingPreviewModel struct {
	ModelID     string          `json:"model_id"`
	PublicName  string          `json:"public_name"`
	Protocol    string          `json:"protocol"`
	RetailPrice RetailPriceView `json:"retail_price"`
}
type PricingPreviewContext struct {
	TenantID               string  `json:"tenant_id"`
	TenantStatus           string  `json:"tenant_status"`
	CurrentPriceGroupID    *string `json:"current_price_group_id"`
	CurrentPricingRevision string  `json:"current_pricing_revision"`
	TargetPriceGroupID     *string `json:"target_price_group_id"`
	TargetGroupState       *string `json:"target_group_state"`
	Assignable             bool    `json:"assignable"`
	AsOf                   string  `json:"as_of"`
}
type PricingPreview struct {
	PricingPage[PricingPreviewModel]
	Context PricingPreviewContext
}

func pricingID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u != uuid.Nil && u.String() == v
}
func pricingLabel(v string) bool {
	if !utf8.ValidString(v) || len(v) < 1 || len(v) > 100 || strings.TrimSpace(v) != v {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func pricingStamp(t time.Time) string { return t.UTC().Format(pricingTimeLayout) }
func pricingUint(n int64) string      { return strconv.FormatInt(n, 10) }
func pricingFrame(domain string, values ...*string) []byte {
	b := append([]byte(domain), 0)
	b = binary.BigEndian.AppendUint32(b, uint32(len(values)))
	for _, v := range values {
		if v == nil {
			b = binary.BigEndian.AppendUint32(b, ^uint32(0))
			continue
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(*v)))
		b = append(b, []byte(*v)...)
	}
	return b
}
func pricingFingerprint(kind, actor, scope, target string, safe map[string]any) string {
	parts := []*string{&kind, &actor, &scope, &target}
	var fields []string
	switch kind {
	case "price_group.create":
		fields = []string{"label"}
	case "price_group.update":
		fields = []string{"expected_revision", "label", "state"}
	case "model_alias.create":
		fields = []string{"model_id", "alias"}
	case "model_alias.retire":
		fields = []string{"alias", "expected_revision"}
	case "group_price.publish":
		fields = []string{"model_id", "schema_id", "schema_digest", "variant_key", "currency", "rates", "effective_at"}
	case "group_price.compile":
		fields = []string{"multiplier", "model_ids", "effective_at", "expected_base_version_ids"}
	case "default_price.publish":
		fields = []string{"model_id", "schema_id", "schema_digest", "variant_key", "currency", "rates", "effective_at"}
	case "cost_price.publish":
		fields = []string{"channel_id", "upstream_model", "schema_id", "schema_digest", "variant_key", "currency", "basis", "rates", "effective_at", "expected_channel_revision"}
	case "tenant_pricing.assign":
		fields = []string{"expected_revision", "price_group_id"}
	}
	for _, f := range fields {
		value := safe[f]
		if kind == "group_price.compile" && f == "expected_base_version_ids" {
			if expected, ok := value.(map[string]string); ok && expected == nil {
				value = nil
			}
		}
		if value == nil {
			parts = append(parts, nil)
		} else {
			v, ok := value.(string)
			if !ok {
				switch value := value.(type) {
				case json.RawMessage:
					v = string(value)
				default:
					raw, err := json.Marshal(value)
					if err != nil {
						v = ""
					} else {
						v = string(raw)
					}
				}
			}
			parts = append(parts, &v)
		}
	}
	sum := sha256.Sum256(pricingFrame("moni-aigate/pricing-command/v1", parts...))
	return hex.EncodeToString(sum[:])
}
func pricingDecodeResult(kind string, raw []byte) (any, error) {
	var dst any
	switch kind {
	case "price_group.create", "price_group.update":
		dst = &GroupView{}
	case "group_price.publish":
		dst = &GroupPriceView{}
	case "group_price.compile":
		dst = &GroupPriceCompileView{}
	case "default_price.publish":
		dst = &DefaultPriceView{}
	case "cost_price.publish":
		dst = &CostPriceView{}
	case "tenant_pricing.assign":
		dst = &AssignmentView{}
	default:
		return nil, pricingFail("pricing_invariant")
	}
	// The snapshot is a closed, immutable union, never a resource re-query.
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(dst) != nil || !validPricingResult(dst, raw) {
		return nil, pricingFail("pricing_invariant")
	}
	return dst, nil
}

func pricingDecimal(v string, positive bool) bool {
	n, e := strconv.ParseInt(v, 10, 64)
	return e == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == v
}
func pricingWireTime(v string) bool {
	t, e := time.Parse(pricingTimeLayout, v)
	return e == nil && !t.IsZero() && pricingStamp(t) == v
}
func validPricingResult(value any, raw []byte) bool {
	var keys []string
	valid := false
	switch v := value.(type) {
	case *GroupView:
		keys = []string{"id", "label", "state", "revision", "created_at"}
		valid = pricingID(v.ID) && pricingLabel(v.Label) && (v.State == "active" || v.State == "retired") && pricingDecimal(v.Revision, true) && pricingWireTime(v.CreatedAt)
	case *GroupPriceView:
		keys = []string{"id", "model_id", "price_group_id", "schema_id", "schema_digest", "variant_key", "currency", "rates", "effective_at", "created_at"}
		valid = pricingID(v.ID) && pricingID(v.ModelID) && pricingID(v.PriceGroupID) && pricingID(v.SchemaID) && pricingDigest(v.SchemaDigest) && v.VariantKey == "default" && v.Currency == "CNY" && pricingWireTime(v.EffectiveAt) && pricingWireTime(v.CreatedAt)
	case *GroupPriceCompileView:
		keys = []string{"group_id", "multiplier", "effective_at", "items"}
		valid = pricingID(v.GroupID) && validGroupMultiplier(v.Multiplier) && pricingWireTime(v.EffectiveAt) && len(v.Items) >= 1 && len(v.Items) <= 100
		seen := make(map[string]bool, len(v.Items))
		for _, item := range v.Items {
			if !pricingID(item.ModelID) || seen[item.ModelID] || !pricingID(item.BasePriceVersionID) || !pricingDecimal(item.BaseInputRate, false) || !pricingDecimal(item.BaseOutputRate, false) || !pricingDecimal(item.InputRate, false) || !pricingDecimal(item.OutputRate, false) || item.PriceVersionID != nil && !pricingID(*item.PriceVersionID) {
				valid = false
				break
			}
			seen[item.ModelID] = true
		}
	case *DefaultPriceView:
		keys = []string{"id", "model_id", "price_group_id", "schema_id", "schema_digest", "variant_key", "currency", "rates", "effective_at", "created_at"}
		valid = pricingID(v.ID) && pricingID(v.ModelID) && v.PriceGroupID == nil && pricingID(v.SchemaID) && pricingDigest(v.SchemaDigest) && v.VariantKey == "default" && v.Currency == "CNY" && pricingWireTime(v.EffectiveAt) && pricingWireTime(v.CreatedAt)
	case *CostPriceView:
		keys = []string{"id", "channel_id", "upstream_model", "schema_id", "schema_digest", "variant_key", "currency", "basis", "rates", "effective_at", "created_at", "expected_channel_revision", "channel_revision"}
		valid = pricingID(v.ID) && pricingID(v.ChannelID) && validUpstreamModel(v.UpstreamModel) && pricingID(v.SchemaID) && pricingDigest(v.SchemaDigest) && v.VariantKey == "default" && v.Currency == "CNY" && (v.Basis == "contract" || v.Basis == "invoice" || v.Basis == "unknown") && pricingDecimal(v.ExpectedChannelRevision, true) && pricingDecimal(v.ChannelRevision, true) && pricingWireTime(v.EffectiveAt) && pricingWireTime(v.CreatedAt)
	case *AssignmentView:
		keys = []string{"tenant_id", "price_group_id", "pricing_revision"}
		valid = pricingID(v.TenantID) && (v.PriceGroupID == nil || pricingID(*v.PriceGroupID)) && pricingDecimal(v.PricingRevision, true)
	}
	_, shape := strictPricingObject(raw, keys...)
	return valid && shape
}
