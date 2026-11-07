package media

func NewLifecycle(profile Profile, input Input) (Lifecycle, error) {
	if !profile.valid || !input.valid {
		return Lifecycle{}, ErrInvalidValue
	}
	if input.profileDigest != profile.digest || input.profile != profile.version || input.variant != profile.variant {
		return Lifecycle{}, ErrBindingMismatch
	}
	return Lifecycle{
		valid:       true,
		profile:     profile,
		input:       input,
		state:       StatePrepared,
		consumption: ConsumptionNoDispatch,
	}, nil
}

// ObserveDispatch records an already-made external dispatch. It never checks
// or grants authorization; DispatchCheck is the closed publication gate.
func (l Lifecycle) ObserveDispatch() (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	if l.state != StatePrepared {
		return l, ErrInvalidState
	}
	l.state = StateSending
	l.consumption = ConsumptionUnknown
	return l, nil
}

func (l Lifecycle) DispatchCheck() error {
	if err := l.validState(); err != nil {
		return err
	}
	if l.state != StatePrepared {
		return ErrInvalidState
	}
	return l.profile.DispatchCheck()
}

func (l Lifecycle) ObserveResult(result InlineResult) (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	if l.state != StateSending {
		return l, ErrInvalidState
	}
	if !result.valid || result.profileDigest != l.profile.digest || result.profile != l.profile.version || result.variant != l.profile.variant || result.size != l.input.size {
		return l, ErrBindingMismatch
	}
	l.state = StateResultReceived
	l.result = result
	return l, nil
}

func NewUsageEvidence(in UsageEvidenceInput) (UsageEvidence, error) {
	if in.Source != "adapter" || !safeReference(in.EventID) {
		return UsageEvidence{}, ErrInvalidUsage
	}
	return UsageEvidence{valid: true, source: in.Source, event: in.EventID}, nil
}

func (l Lifecycle) ConfirmUsage(evidence UsageEvidence) (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	if l.state != StateResultReceived {
		return l, ErrInvalidState
	}
	if !evidence.valid {
		return l, ErrUsageUnconfirmed
	}
	l.state = StateCompleted
	l.consumption = ConsumptionConfirmed
	l.usage = evidence
	return l, nil
}

func (l Lifecycle) MarkConsumptionUnknown() (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	if l.state != StateSending && l.state != StateResultReceived {
		return l, ErrInvalidState
	}
	l.state = StateConsumptionUnknown
	l.consumption = ConsumptionUnknown
	return l, nil
}

func (l Lifecycle) Fail(kind FailureKind) (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	switch l.state {
	case StatePrepared:
		if kind != FailureInputRejected && kind != FailureResourceLimit && kind != FailureProfileUnpublished {
			return l, ErrInvalidState
		}
		l.consumption = ConsumptionNoDispatch
	case StateSending, StateResultReceived:
		if kind != FailureInvalidResult && kind != FailureTransport && kind != FailureUsageMissing && kind != FailureCancelled {
			return l, ErrInvalidState
		}
		l.consumption = ConsumptionUnknown
	default:
		return l, ErrInvalidState
	}
	l.state = StateFailed
	l.failure = kind
	return l, nil
}

func (l Lifecycle) Cancel() (Lifecycle, error) {
	if err := l.validState(); err != nil {
		return l, err
	}
	switch l.state {
	case StatePrepared:
		l.consumption = ConsumptionNoDispatch
	case StateSending, StateResultReceived:
		l.consumption = ConsumptionUnknown
	default:
		return l, ErrInvalidState
	}
	l.state = StateCancelled
	l.failure = FailureCancelled
	return l, nil
}

func (l Lifecycle) State() (ExecutionState, error) {
	if err := l.validState(); err != nil {
		return "", err
	}
	return l.state, nil
}

func (l Lifecycle) Consumption() (ConsumptionStatus, error) {
	if err := l.validState(); err != nil {
		return "", err
	}
	return l.consumption, nil
}

func (l Lifecycle) Failure() (FailureKind, error) {
	if err := l.validState(); err != nil {
		return "", err
	}
	return l.failure, nil
}

func (l Lifecycle) Result() (InlineResult, error) {
	if err := l.validState(); err != nil {
		return InlineResult{}, err
	}
	if !l.result.valid {
		return InlineResult{}, ErrInvalidValue
	}
	return l.result, nil
}

func (l Lifecycle) validState() error {
	if !l.valid || !l.profile.valid || !l.input.valid {
		return ErrInvalidValue
	}
	return nil
}
