package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

const (
	measurementVersion    = 2
	phase2BaselineVersion = 1
)

type Config struct {
	SchemaVersion   int               `json:"schema_version"`
	RunID           string            `json:"run_id"`
	Source          SourceConfig      `json:"source"`
	Gateway         GatewayConfig     `json:"gateway"`
	Mock            MockConfig        `json:"mock"`
	Resources       ResourcesConfig   `json:"resources"`
	Load            LoadShape         `json:"load"`
	Connection      ConnectionConfig  `json:"connection"`
	Timing          TimingConfig      `json:"timing"`
	CommercialChain CommercialChain   `json:"commercial_chain"`
	Correctness     CorrectnessConfig `json:"correctness"`
}

type SourceConfig struct {
	CommitSHA    string   `json:"commit_sha"`
	TreeSHA256   string   `json:"tree_sha256"`
	TreeClean    bool     `json:"tree_clean"`
	BuildCommand string   `json:"build_command"`
	BuildArgs    []string `json:"build_args"`
}

type GatewayConfig struct {
	BaseURL   string          `json:"base_url"`
	APIKeyEnv string          `json:"api_key_env"`
	Path      string          `json:"path"`
	Body      json.RawMessage `json:"body"`
}

type MockConfig struct {
	ReceiptsURL string `json:"receipts_url"`
	ConfigPath  string `json:"config_path"`
}

type ResourcesConfig struct {
	Application   ResourceConfig `json:"application"`
	PostgreSQL    ResourceConfig `json:"postgresql"`
	LoadGenerator ResourceConfig `json:"load_generator"`
	Mock          ResourceConfig `json:"mock"`
}

type ResourceConfig struct {
	CPU             string `json:"cpu"`
	Memory          string `json:"memory"`
	ConnectionLimit int    `json:"connection_limit,omitempty"`
	Verified        bool   `json:"verified"`
	SamplesJSONL    string `json:"samples_jsonl,omitempty"`
}

type LoadShape struct {
	ConcurrentClients     int      `json:"concurrent_clients"`
	ExpectedActiveStreams int      `json:"expected_active_streams"`
	WarmupSeconds         int      `json:"warmup_seconds"`
	SampleSeconds         int      `json:"sample_seconds"`
	Repeats               int      `json:"repeats"`
	RotationOrder         []string `json:"rotation_order"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds"`
	UpstreamFault         string   `json:"upstream_fault"`
}

type ConnectionConfig struct {
	TLS                bool   `json:"tls"`
	HTTPVersion        string `json:"http_version"`
	DisableKeepAlives  bool   `json:"disable_keep_alives"`
	MaxIdleConnections int    `json:"max_idle_connections"`
	MaxConnsPerHost    int    `json:"max_conns_per_host"`
	ProviderRetries    int    `json:"provider_retries"`
	Plugins            string `json:"plugins"`
	Cache              string `json:"cache"`
}

type TimingConfig struct {
	ClockSource  string `json:"clock_source"`
	SkewVerified bool   `json:"skew_verified"`
}

type CommercialChain struct {
	NormalReleaseMain     bool `json:"normal_release_main"`
	Authentication        bool `json:"authentication"`
	WalletHardReservation bool `json:"wallet_hard_reservation"`
	KeyLimits             bool `json:"key_limits"`
	PersistentAttempts    bool `json:"persistent_attempts"`
	FinalEvidence         bool `json:"final_evidence"`
	UniqueSettlement      bool `json:"unique_settlement"`
	Audit                 bool `json:"audit"`
}

type CorrectnessConfig struct {
	EvidencePath string `json:"evidence_path,omitempty"`
}

type Sample struct {
	Repeat                 int                    `json:"repeat"`
	Phase                  string                 `json:"phase"`
	Worker                 int                    `json:"worker"`
	Sequence               int64                  `json:"sequence"`
	Nonce                  string                 `json:"nonce"`
	RequestID              string                 `json:"request_id,omitempty"`
	StartedAt              time.Time              `json:"started_at"`
	FirstByteAt            *time.Time             `json:"first_byte_at,omitempty"`
	FirstContentAt         *time.Time             `json:"first_content_at,omitempty"`
	EndedAt                time.Time              `json:"ended_at"`
	HTTPStatus             int                    `json:"http_status"`
	HTTPVersion            string                 `json:"http_version,omitempty"`
	TLS                    bool                   `json:"tls"`
	Done                   bool                   `json:"done"`
	Rejection              string                 `json:"rejection,omitempty"`
	Error                  string                 `json:"error,omitempty"`
	TTFBMS                 *float64               `json:"ttfb_ms,omitempty"`
	TTFCMS                 *float64               `json:"ttfc_ms,omitempty"`
	FullMS                 float64                `json:"full_ms"`
	MockReceipt            *mockupstream.Receipt  `json:"mock_receipt,omitempty"`
	MockReceipts           []mockupstream.Receipt `json:"mock_receipts,omitempty"`
	MockReceiptCount       int                    `json:"mock_receipt_count"`
	MockWaitToFirstEventMS *float64               `json:"mock_wait_to_first_event_ms,omitempty"`
	MockWaitToContentMS    *float64               `json:"mock_wait_to_content_ms,omitempty"`
	ExtraTTFBMS            *float64               `json:"extra_ttfb_ms,omitempty"`
	ExtraTTFCMS            *float64               `json:"extra_ttfc_ms,omitempty"`
	JoinError              string                 `json:"join_error,omitempty"`
}

func (s Sample) Successful() bool {
	return s.HTTPStatus == 200 && s.Done && s.Error == ""
}

type RequestIdentity struct {
	Sequence  int64  `json:"sequence"`
	Nonce     string `json:"nonce"`
	RequestID string `json:"request_id"`
}

type CorrectnessEvidence struct {
	Status                    string            `json:"status"`
	RunID                     string            `json:"run_id"`
	SourceCommitSHA           string            `json:"source_commit_sha"`
	SourceTreeSHA256          string            `json:"source_tree_sha256"`
	InputSHA256               string            `json:"input_sha256"`
	ReportSHA256              string            `json:"report_sha256"`
	Requests                  []RequestIdentity `json:"requests"`
	RequestsChecked           int               `json:"requests_checked"`
	OverchargeDetected        bool              `json:"overcharge_detected"`
	ResidualHolds             int64             `json:"residual_holds"`
	ResidualPermits           int64             `json:"residual_permits"`
	UnauthorizedDispatches    int64             `json:"unauthorized_dispatches"`
	UnknownConsumptionReplays int64             `json:"unknown_consumption_replays"`
	LedgerBalanced            bool              `json:"ledger_balanced"`
}

func (e CorrectnessEvidence) ValidateBinding(cfg Config, inputSHA, reportSHA string, requests []RequestIdentity) error {
	if e.RunID != cfg.RunID {
		return fmt.Errorf("correctness evidence run_id mismatch")
	}
	if e.SourceCommitSHA != cfg.Source.CommitSHA || e.SourceTreeSHA256 != cfg.Source.TreeSHA256 {
		return fmt.Errorf("correctness evidence source identity mismatch")
	}
	if e.InputSHA256 != inputSHA {
		return fmt.Errorf("correctness evidence redacted input digest mismatch")
	}
	if e.ReportSHA256 != reportSHA {
		return fmt.Errorf("correctness evidence measurement report digest mismatch")
	}
	if e.RequestsChecked != len(requests) || !exactRequestIdentities(e.Requests, requests) {
		return fmt.Errorf("correctness evidence exact request identities mismatch")
	}
	return nil
}

func (e CorrectnessEvidence) Passed(cfg Config, inputSHA, reportSHA string, requests []RequestIdentity) bool {
	return e.ValidateBinding(cfg, inputSHA, reportSHA, requests) == nil && e.Status == "passed" && !e.OverchargeDetected && e.ResidualHolds == 0 && e.ResidualPermits == 0 && e.UnauthorizedDispatches == 0 && e.UnknownConsumptionReplays == 0 && e.LedgerBalanced
}

func requestIdentities(samples []Sample) []RequestIdentity {
	requests := make([]RequestIdentity, 0, len(samples))
	for _, sample := range samples {
		requests = append(requests, RequestIdentity{Sequence: sample.Sequence, Nonce: sample.Nonce, RequestID: sample.RequestID})
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].Sequence < requests[j].Sequence })
	return requests
}

func exactRequestIdentities(got, want []RequestIdentity) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
