package payments

import "net/url"

// SandboxNotificationsEnabled discloses only whether a successfully constructed
// Service owns an environment. It does not expose configuration or key material.
func (s *Service) SandboxNotificationsEnabled() bool {
	return s != nil && s.runtime != nil && s.runtime.hasEnvironment()
}

// ValidateApplicationAddress pins the actual listener to the immutable callback
// origin before any pool is opened. Ordinary off has no sandbox listener pin.
func ValidateApplicationAddress(o Options, address string) error {
	if !o.valid || o.component != applicationComponent {
		return ErrPaymentsConfiguration
	}
	if o.disabled {
		return nil
	}
	origin, err := url.Parse(o.proof.AppListenOrigin)
	if err != nil || origin.Host != address || origin.Scheme != "http" {
		return ErrPaymentsConfiguration
	}
	return nil
}
