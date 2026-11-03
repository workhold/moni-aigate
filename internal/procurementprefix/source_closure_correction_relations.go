package procurementprefix

// Correction relations bind explicit typed causes to raw immutable originals.
// They do not certify the historical permission or business eligibility to act.
type sourceCorrectionClaim struct {
	table, kind, operation, correction string
	raw                                sourceRawRow
	tenant, request, attempt           string
}

type sourceCorrectionExpected struct{ amount, basis, evidence string }

type sourceCorrectionRelations struct {
	s           *sourceClosureState
	err         error
	index       map[string]map[string][]sourceRawRow
	originalOps map[string][]sourceRawRow
	byID, byOp  map[string][]int
	claims      []sourceCorrectionClaim
}

func (s *sourceClosureState) correctionRelationRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	c := &sourceCorrectionRelations{s: s, index: make(map[string]map[string][]sourceRawRow), originalOps: make(map[string][]sourceRawRow), byID: make(map[string][]int), byOp: make(map[string][]int)}
	c.prepare()
	c.register()
	c.checkClaims()
	c.reverse()
	if c.err != nil {
		return c.err
	}
	return s.w.b.check()
}

func (c *sourceCorrectionRelations) tick() bool {
	if c.err == nil {
		c.err = c.s.w.tick()
	}
	return c.err == nil
}

func (c *sourceCorrectionRelations) prepare() {
	for _, table := range []string{"pp_approval_consumptions", "pp_unmanaged_correction_sources", "provider_cost_corrections", "upstream_attempts", "procurement_cost_proposals", "pp_proposal_births"} {
		if !c.tick() {
			return
		}
		c.index[table] = make(map[string][]sourceRawRow)
		for _, raw := range c.s.tables[table] {
			if !c.tick() {
				return
			}
			c.index[table][raw.key] = append(c.index[table][raw.key], raw)
			if table == "provider_cost_corrections" {
				key := tupleKey(sourceText(raw.value, "operation_id"))
				c.originalOps[key] = append(c.originalOps[key], raw)
			}
		}
	}
}

func (c *sourceCorrectionRelations) register() {
	for _, table := range []string{"pp_approval_consumptions", "pp_unmanaged_correction_sources"} {
		for _, raw := range c.s.tables[table] {
			if !c.tick() {
				return
			}
			claim := sourceCorrectionClaim{table: table, raw: raw, correction: sourceText(raw.value, "provider_correction_id"), tenant: sourceText(raw.value, "tenant_id"), request: sourceText(raw.value, "request_id"), attempt: sourceText(raw.value, "attempt_id")}
			if table == "pp_approval_consumptions" {
				claim.kind, claim.operation = "approval_consumed", "procurement:"+sourceText(raw.value, "approval_operation_id")
			} else {
				claim.kind, claim.operation = "unmanaged_correction", sourceText(raw.value, "operation_id")
			}
			i := len(c.claims)
			c.claims = append(c.claims, claim)
			c.byID[tupleKey(claim.correction)] = append(c.byID[tupleKey(claim.correction)], i)
			c.byOp[tupleKey(claim.operation)] = append(c.byOp[tupleKey(claim.operation)], i)
		}
	}
}

func (c *sourceCorrectionRelations) one(table, id string) (sourceRawRow, bool) {
	if !c.tick() {
		return sourceRawRow{}, false
	}
	rows := c.index[table][tupleKey(id)]
	if len(rows) != 1 {
		return sourceRawRow{}, false
	}
	return rows[0], true
}

func (c *sourceCorrectionRelations) equal(pairs ...[2]string) bool {
	valid := true
	for _, pair := range pairs {
		if !c.tick() {
			return false
		}
		if pair[0] != pair[1] {
			valid = false
		}
	}
	return valid
}

func (c *sourceCorrectionRelations) expected(claim sourceCorrectionClaim) (sourceCorrectionExpected, bool) {
	attemptRaw, found := c.one("upstream_attempts", claim.attempt)
	if !found {
		return sourceCorrectionExpected{}, false
	}
	a := attemptRaw.value.Interface().(UpstreamAttemptRow)
	if !c.equal([2]string{a.ID, claim.attempt}, [2]string{a.TenantID, claim.tenant}, [2]string{a.RequestID, claim.request}) {
		return sourceCorrectionExpected{}, false
	}
	if claim.kind == "unmanaged_correction" {
		u := claim.raw.value.Interface().(UnmanagedCorrectionSourceRow)
		return sourceCorrectionExpected{u.Amount, u.Basis, u.EvidenceRef}, true
	}
	s := claim.raw.value.Interface().(ApprovalConsumptionRow)
	proposalRaw, proposalOK := c.one("procurement_cost_proposals", s.ProposalID)
	birthRaw, birthOK := c.one("pp_proposal_births", s.ProposalBirthID)
	if !proposalOK || !birthOK {
		return sourceCorrectionExpected{}, false
	}
	p := proposalRaw.value.Interface().(ProcurementCostProposalRow)
	b := birthRaw.value.Interface().(ProposalBirthRow)
	valid := c.equal(
		[2]string{p.ID, s.ProposalID}, [2]string{p.TenantID, s.TenantID}, [2]string{p.AttemptID, s.AttemptID}, [2]string{p.Requester, s.Requester}, [2]string{p.State, "approved"},
		[2]string{b.ProposalID, s.ProposalID}, [2]string{b.TenantID, s.TenantID}, [2]string{b.RequestID, s.RequestID}, [2]string{b.AttemptID, s.AttemptID}, [2]string{b.Requester, s.Requester},
		[2]string{p.ExpectedReservationRevision, b.ReservationRevision}, [2]string{p.ExpectedEvidenceCount, b.MemberCount},
	)
	for _, field := range []struct {
		got  *string
		want string
	}{{p.Approver, s.Approver}, {p.ApprovalOperationID, s.ApprovalOperationID}} {
		if !c.tick() {
			return sourceCorrectionExpected{}, false
		}
		if field.got == nil || *field.got != field.want {
			valid = false
		}
	}
	if !c.tick() || !finite(p.ApprovedAt) {
		valid = false
	}
	if !valid {
		return sourceCorrectionExpected{}, false
	}
	return sourceCorrectionExpected{p.Amount, p.Basis, p.EvidenceRef}, true
}

func (c *sourceCorrectionRelations) pointer(raw sourceRawRow, claim sourceCorrectionClaim) bool {
	for _, field := range [][2]string{{"pp_root_id", sourceText(claim.raw.value, "root_id")}, {"pp_root_step", sourceText(claim.raw.value, "step_no")}, {"pp_source_kind", claim.kind}, {"pp_source_id", sourceText(claim.raw.value, "id")}, {"pp_source_role", "correction"}, {"pp_created_xid", sourceText(claim.raw.value, "created_xid")}} {
		if !c.tick() {
			return false
		}
		got := sourceOptional(raw.value, field[0])
		if got == nil || *got != field[1] {
			return false
		}
	}
	return true
}

func (c *sourceCorrectionRelations) checkClaims() {
	for _, claim := range c.claims {
		if !c.tick() {
			return
		}
		originals := c.index["provider_cost_corrections"][tupleKey(claim.correction)]
		peers := c.originalOps[tupleKey(claim.operation)]
		valid := len(c.index[claim.table][claim.raw.key]) == 1 && len(c.byID[tupleKey(claim.correction)]) == 1 && len(c.byOp[tupleKey(claim.operation)]) == 1 && len(originals) == 1 && len(peers) == 1 && peers[0].key == tupleKey(claim.correction)
		if valid {
			expected, found := c.expected(claim)
			o := originals[0].value.Interface().(ProviderCostCorrectionRow)
			valid = found && c.equal([2]string{sourceText(claim.raw.value, "step_kind"), claim.kind}, [2]string{o.ID, claim.correction}, [2]string{o.TenantID, claim.tenant}, [2]string{o.AttemptID, claim.attempt}, [2]string{o.OperationID, claim.operation}, [2]string{o.Amount, expected.amount}, [2]string{o.Basis, expected.basis}, [2]string{o.EvidenceRef, expected.evidence}) && finite(o.CreatedAt) && c.pointer(originals[0], claim)
		}
		if c.err != nil {
			return
		}
		if !valid {
			c.mark(claim.table, claim.raw)
			c.markOriginals(originals)
			c.markOriginals(peers)
		}
	}
}

func (c *sourceCorrectionRelations) mark(table string, raw sourceRawRow) {
	if c.tick() {
		c.s.mark("pointer", table, raw.key)
	}
}

func (c *sourceCorrectionRelations) markOriginals(rows []sourceRawRow) {
	for _, raw := range rows {
		if !c.tick() {
			return
		}
		c.mark("provider_cost_corrections", raw)
	}
}

func (c *sourceCorrectionRelations) reverse() {
	for _, raw := range c.s.tables["provider_cost_corrections"] {
		if !c.tick() {
			return
		}
		tracked := false
		for _, field := range []string{"pp_root_id", "pp_root_step", "pp_source_kind", "pp_source_id", "pp_source_role", "pp_created_xid"} {
			if !c.tick() {
				return
			}
			tracked = sourceOptional(raw.value, field) != nil || tracked
		}
		claims := c.byID[raw.key]
		if tracked && len(claims) != 1 {
			c.mark("provider_cost_corrections", raw)
			for _, i := range claims {
				if !c.tick() {
					return
				}
				c.mark(c.claims[i].table, c.claims[i].raw)
			}
		}
	}
}
