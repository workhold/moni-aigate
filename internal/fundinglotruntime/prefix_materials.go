package fundinglotruntime

import (
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotprefixcompat"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

func prefixTuple(t materialTuple) fundinglotprefixcompat.Tuple {
	return fundinglotprefixcompat.Tuple{Expected: t.Expected, Schema: t.Schema, Roles: t.Roles}
}
func runtimeTuple(t fundinglotprefixcompat.Tuple) materialTuple {
	return materialTuple{Expected: t.Expected, Schema: t.Schema, Roles: t.Roles}
}

func matchRuntimeCompatibility(b fundinglotproof.V2Bindings, current currentCatalog) (compatibilityRecord, error) {
	if current.profile != fundinglotcatalog.PeriodicPrefixV3 {
		return matchCompatibility(b, current)
	}
	r, err := fundinglotprefixcompat.Match(b, current.version, prefixTuple(current.tuple))
	if err != nil {
		return compatibilityRecord{}, ErrRuntime
	}
	return compatibilityRecord{Mode: r.Mode, Variant: r.Variant, Historical: runtimeTuple(r.Historical), Current: runtimeTuple(r.Current)}, nil
}

func runtimeProfileStateValid(s *runtimeProofState) bool {
	switch s.catalog.Profile() {
	case fundinglotcatalog.WalletReadV2:
		return s.basisDescriptorSHA == basisDescriptorSHA && s.prefixReport == (fundinglotscan.PrefixWalletReport{})
	case fundinglotcatalog.PeriodicPrefixV3:
		mode, name := fundinglotscan.ModeOrdinary, "ordinary"
		if s.pins.Mode == fundinglotcatalog.Sandbox {
			mode, name = fundinglotscan.ModeSandbox, "sandbox"
		} else if s.pins.Mode != fundinglotcatalog.Ordinary {
			return false
		}
		if s.report != (fundinglotscan.WalletReport{}) || !runtimePrefixReportComplete(s.prefixReport, mode) || s.prefixReport.Periodic != s.prefixReport.PeriodicPrefix.Base {
			return false
		}
		r, err := fundinglotprefixcompat.MatchTuples(name, s.basisDescriptorSHA, prefixTuple(s.compatibility.Historical), prefixTuple(s.compatibility.Current))
		return err == nil && r.Mode == s.compatibility.Mode && r.Variant == s.compatibility.Variant
	default:
		return false
	}
}
