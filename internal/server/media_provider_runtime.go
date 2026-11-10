package server

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/media"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/realtime"
)

var errMediaProviderUnavailable = errors.New("provider_unavailable")

type mediaProviderRuntime struct {
	mediaAdapter    provider.SynchronousMediaAdapter
	imageProfile    media.Profile
	imageService    *media.Orchestrator
	realtimeAdapter *provider.MockRealtimeAdapter
	realtimeService *realtime.Service
	taskAdapter     provider.TaskAdapter
}

// newMediaProviderRuntime is the only production wiring point for the
// repository's media fixtures. A loaded inner-test Config produces no
// adapter, so all media entrypoints fail closed before Reserve. Real adapters
// are intentionally absent until a separate qualification slice installs one.
func newMediaProviderRuntime(c config.Config) mediaProviderRuntime {
	if !c.MockMediaProvidersEnabled() {
		return mediaProviderRuntime{}
	}
	mediaAdapter := newDefaultMediaAdapter()
	imageProfile, imageService := newImageRuntime(mediaAdapter, true)
	realtimeAdapter := provider.NewMockRealtimeAdapter()
	realtimeService, err := realtime.NewService(realtime.ServiceOptions{
		Store:        realtime.NewMemoryStore(),
		Adapter:      realtimeAdapter,
		RequiredMode: realtime.ProviderModeMock,
	})
	if err != nil {
		panic("invalid deterministic media wiring: " + err.Error())
	}
	return mediaProviderRuntime{
		mediaAdapter:    mediaAdapter,
		imageProfile:    imageProfile,
		imageService:    imageService,
		realtimeAdapter: realtimeAdapter,
		realtimeService: realtimeService,
		taskAdapter:     provider.NewMockTaskAdapter(),
	}
}

func (s *Server) expectedMediaProviderMode() provider.ProviderMode {
	if s != nil && (s.Config.MockMediaProvidersEnabled() || s.Config.MediaProviderProfile == "") {
		return provider.ProviderModeMock
	}
	return provider.ProviderModeReal
}

func (s *Server) mediaProviderReady(adapter any) bool {
	if s == nil || adapter == nil {
		return false
	}
	mode, ok := provider.AdapterMode(adapter)
	if !ok {
		return false
	}
	// An empty profile is used only by direct protocol tests that inject a
	// mock adapter. Config.Load always fills it with an explicit profile.
	if s.Config.MediaProviderProfile == "" {
		return mode == provider.ProviderModeMock
	}
	return s.Config.MediaProviderProfile != config.MediaProviderProfileInnerTest && mode == s.expectedMediaProviderMode()
}

func (s *Server) realtimeProviderReady() bool {
	if s == nil || s.RealtimeAdapter == nil {
		return false
	}
	if s.Config.MediaProviderProfile == "" {
		return s.Realtime != nil
	}
	return s.Config.MockMediaProvidersEnabled()
}

func (s *Server) mockMediaProfile() bool {
	return s != nil && s.Config.MockMediaProvidersEnabled()
}

func (s *Server) mediaProviderMode() string {
	if s != nil && s.mockMediaProfile() {
		return string(provider.ProviderModeMock)
	}
	return string(provider.ProviderModeReal)
}

func (s *Server) providerErrorCode() string { return errMediaProviderUnavailable.Error() }

// Mock output is a workflow fixture, not supplier evidence. It is released
// through the normal settlement path with no customer charge and no supplier
// cost amount, so a business QA run cannot spend real wallet balance.
func (s *Server) mediaFinalizationValues(outcome, usageState string, quantity int64) (string, string, int64) {
	if s.mockMediaProfile() && outcome == "completed" && usageState == "confirmed_final" {
		return outcome, "missing", 0
	}
	return outcome, usageState, quantity
}
