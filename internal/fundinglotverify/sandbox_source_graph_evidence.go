package fundinglotverify

import (
	"sort"
	"time"
)

func sandboxGraphBound(q sandboxGraphRequest, b SandboxGraphCommandOutboxRow, o SandboxGraphOrderRow) bool {
	return b.ExternalCommandID != nil && q.EnvironmentID == o.EnvironmentID && q.MerchantID == o.MerchantID && q.MerchantOrderID == o.MerchantOrderID && q.CommandID == *b.ExternalCommandID
}
func sandboxGraphEcho(r sandboxGraphReply, q sandboxGraphRequest) bool {
	return r.QueryID == q.QueryID && r.EnvironmentID == q.EnvironmentID && r.MerchantID == q.MerchantID && r.MerchantOrderID == q.MerchantOrderID && r.CommandID == q.CommandID && r.CommandKind == q.CommandKind && r.ParameterDigest == q.ParameterDigest
}
func sandboxGraphCode(s *string, values ...string) bool {
	return s != nil && sandboxGraphHas(*s, values...)
}
func sandboxGraphFailure(s *string) bool {
	return sandboxGraphCode(s, "network", "timeout", "auth", "protocol", "environment", "temporary")
}
func (g *sandboxGraphData) commandsAndOriginals() {
	for _, b := range sandboxGraphRows[SandboxGraphCommandOutboxRow](g, "CommandOutbox") {
		if !g.tick() {
			return
		}
		o, ok := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", b.OrderID)
		n := sandboxGraphN(b.AttemptCount)
		valid := fundingGraphUUID(b.ID) && ok && sandboxGraphUint(b.Generation, 63, false) && b.Generation == b.AttemptCount && n >= 0
		if b.Kind == "query" {
			p, pok := sandboxGraphGet[SandboxGraphCommandOutboxRow](g, "CommandOutbox", sandboxGraphPtr(b.OriginalOutboxID))
			valid = valid && n <= 20 && pok && sandboxGraphHas(p.Kind, "checkout", "cancel") && p.OrderID == b.OrderID && b.ExternalCommandID == nil && b.CanonicalParameters == nil && b.ParameterDigest == nil && b.LastSubmissionUnknownAt == nil && b.State != "submission_unknown"
		} else if sandboxGraphHas(b.Kind, "checkout", "cancel") {
			valid = valid && n <= 10 && b.ExternalCommandID != nil && b.CanonicalParameters != nil && b.ParameterDigest != nil && b.OriginalOutboxID == nil && b.CurrentQueryID == nil
			if b.CanonicalParameters != nil {
				q, qok := g.request(*b.CanonicalParameters, b.Kind)
				bound := qok && sandboxGraphBound(q, b, o) && sandboxGraphPtr(b.ParameterDigest) == sandboxGraphSHA(*b.CanonicalParameters)
				if b.Kind == "checkout" {
					bound = bound && q.Amount == o.Amount && q.Currency == o.Currency && q.CreatedAt == o.CreatedAt.UTC().Format(sandboxGraphTimeLayout) && q.ExpiresAt == o.ExpiresAt.UTC().Format(sandboxGraphTimeLayout)
				} else {
					bound = bound && g.exists("CancelIntents", b.OrderID)
				}
				if !bound {
					g.bad("CommandParameterRows", "CommandOutbox", b.ID)
				} else {
					g.mutations[b.ID] = q
				}
			}
		} else {
			valid = false
			g.bad("InvalidEnumRows", "CommandOutbox", b.ID)
		}
		valid = valid && sandboxGraphHas(b.State, "pending", "retry_wait", "leased", "completed", "submission_unknown", "review_required") && ((b.State == "leased") == (b.LeaseUntil != nil))
		if b.LastErrorCode != nil && !sandboxGraphCode(b.LastErrorCode, "network", "timeout", "auth", "protocol", "environment", "temporary", "permanent", "attempts_exhausted") {
			valid = false
		}
		if b.State == "pending" {
			valid = valid && n == 0 && b.CurrentQueryID == nil && b.LeaseUntil == nil && b.LastErrorCode == nil && b.LastSubmissionUnknownAt == nil
		} else {
			valid = valid && n > 0
		}
		if b.State == "leased" || b.State == "completed" {
			valid = valid && b.LastErrorCode == nil
		}
		if b.State == "submission_unknown" {
			valid = valid && b.LastSubmissionUnknownAt != nil && (n < 10 && sandboxGraphFailure(b.LastErrorCode) || n == 10 && sandboxGraphCode(b.LastErrorCode, "attempts_exhausted"))
		}
		if b.State == "retry_wait" {
			if b.Kind == "query" {
				valid = valid && n < 20 && (b.LastErrorCode == nil || sandboxGraphFailure(b.LastErrorCode))
			} else {
				valid = valid && n < 10 && b.LastSubmissionUnknownAt != nil && b.LastErrorCode == nil
			}
		}
		if b.State == "review_required" {
			if b.Kind == "query" {
				valid = valid && (n < 20 && sandboxGraphCode(b.LastErrorCode, "permanent") || n == 20 && sandboxGraphCode(b.LastErrorCode, "attempts_exhausted"))
			} else {
				valid = valid && sandboxGraphCode(b.LastErrorCode, "permanent")
			}
		}
		if !valid {
			g.bad("CommandStateRows", "CommandOutbox", b.ID)
		}
	}
	for _, o := range sandboxGraphRows[SandboxGraphOrderRow](g, "Orders") {
		if !g.tick() {
			return
		}
		bs := sandboxGraphRelated[SandboxGraphCommandOutboxRow](g, "CommandOutbox", []string{"OrderID", "Kind"}, o.ID, "checkout")
		if len(bs) != 1 {
			g.bad("OrderRows", "Orders", o.ID)
		}
		cancel := g.exists("CancelIntents", o.ID)
		cs := sandboxGraphRelated[SandboxGraphCommandOutboxRow](g, "CommandOutbox", []string{"OrderID", "Kind"}, o.ID, "cancel")
		if cancel && len(cs) != 1 || !cancel && len(cs) != 0 {
			g.bad("CancelIntentRows", "Orders", o.ID)
		}
	}
	for _, a := range sandboxGraphRows[SandboxGraphQueryAttemptRow](g, "QueryAttempts") {
		if !g.tick() {
			return
		}
		s, sok := sandboxGraphGet[SandboxGraphCommandOutboxRow](g, "CommandOutbox", a.SupervisorID)
		b, bok := sandboxGraphGet[SandboxGraphCommandOutboxRow](g, "CommandOutbox", sandboxGraphPtr(s.OriginalOutboxID))
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", b.OrderID)
		q, qok := g.request(a.CanonicalRequest, "query")
		valid := sok && bok && ook && s.Kind == "query" && s.OrderID == b.OrderID && qok && q.QueryID == a.QueryID && sandboxGraphBound(q, b, o) && q.CommandKind == b.Kind && q.ParameterDigest == sandboxGraphPtr(b.ParameterDigest) && sandboxGraphSHA(a.CanonicalRequest) == a.RequestDigest && sandboxGraphMoney(a.CreatedGeneration) && sandboxGraphMoney(a.MutationGeneration) && sandboxGraphN(a.CreatedGeneration) <= sandboxGraphN(s.Generation) && sandboxGraphN(a.MutationGeneration) <= sandboxGraphN(b.Generation)
		if !valid {
			g.bad("QueryAttemptRows", "QueryAttempts", a.QueryID)
		} else {
			g.queries[a.QueryID] = q
		}
	}
	for _, r := range sandboxGraphRows[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries") {
		if !g.tick() {
			return
		}
		q, qok := g.queries[r.QueryID]
		v, vok := g.reply(r.CanonicalBody, true)
		if !qok || !vok || !sandboxGraphEcho(v, q) || r.SnapshotDigest != sandboxGraphSHA(r.CanonicalBody) || !g.keys[r.VerifiedKeyID] {
			g.bad("VerifiedQueryRows", "VerifiedQueries", r.ID)
		} else {
			g.replies[r.ID] = v
		}
	}
	for _, n := range sandboxGraphRows[SandboxGraphInboxRow](g, "Inbox") {
		if !g.tick() {
			return
		}
		v, ok := g.notification(n.CanonicalBody)
		if !ok || v.EventID != n.EventID || v.EnvironmentID != n.EnvironmentID || v.MerchantID != n.MerchantID || n.EnvironmentID != g.env.ID || n.MerchantID != g.env.MerchantID || n.BusinessDigest != sandboxGraphSHA(n.CanonicalBody) || !g.keys[n.VerifiedKeyID] {
			g.bad("InboxRows", "Inbox", n.ID)
		}
	}
	for _, r := range sandboxGraphRows[SandboxGraphObservationRow](g, "Observations") {
		if !g.tick() {
			return
		}
		b, bok := sandboxGraphGet[SandboxGraphCommandOutboxRow](g, "CommandOutbox", r.OutboxID)
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", b.OrderID)
		v, vok := g.reply(r.CanonicalReply, false)
		valid := bok && ook && sandboxGraphHas(b.Kind, "checkout", "cancel") && vok && v.EnvironmentID == o.EnvironmentID && v.MerchantID == o.MerchantID && v.MerchantOrderID == o.MerchantOrderID && v.CommandID == sandboxGraphPtr(b.ExternalCommandID) && v.CommandKind == b.Kind && v.ParameterDigest == sandboxGraphPtr(b.ParameterDigest) && sandboxGraphMoney(r.Generation) && sandboxGraphN(r.Generation) <= sandboxGraphN(b.Generation) && r.ReplyDigest == sandboxGraphSHA(r.CanonicalReply)
		if !valid {
			g.bad("ObservationRows", "Observations", r.ID)
		} else {
			g.observations[r.ID] = v
		}
	}
	for _, x := range sandboxGraphRows[SandboxGraphEventConflictRow](g, "EventConflicts") {
		if !g.tick() {
			return
		}
		p, ok := sandboxGraphGet[SandboxGraphInboxRow](g, "Inbox", x.InboxID)
		if !ok || !sandboxGraphHex(x.ConflictingDigest) || x.ConflictingDigest == p.BusinessDigest {
			g.bad("ConflictRows", "EventConflicts", x.ID)
		}
	}
	for _, x := range sandboxGraphRows[SandboxGraphQueryConflictRow](g, "QueryConflicts") {
		if !g.tick() {
			return
		}
		p, ok := sandboxGraphGet[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", x.VerifiedQueryID)
		if !ok || !sandboxGraphHex(x.ConflictingDigest) || x.ConflictingDigest == p.SnapshotDigest {
			g.bad("ConflictRows", "QueryConflicts", x.ID)
		}
	}
	for _, b := range sandboxGraphRows[SandboxGraphCommandOutboxRow](g, "CommandOutbox") {
		if !g.tick() {
			return
		}
		if b.Kind == "query" {
			children := sandboxGraphRelated[SandboxGraphQueryAttemptRow](g, "QueryAttempts", []string{"SupervisorID"}, b.ID)
			sort.Slice(children, func(i, j int) bool {
				return sandboxGraphN(children[i].CreatedGeneration) < sandboxGraphN(children[j].CreatedGeneration)
			})
			lastMutation := int64(0)
			max := int64(0)
			var current SandboxGraphQueryAttemptRow
			pointerOK := true
			for _, a := range children {
				if !g.tick() {
					return
				}
				mutation := sandboxGraphN(a.MutationGeneration)
				if mutation < lastMutation {
					pointerOK = false
					g.bad("QueryAttemptRows", "QueryAttempts", a.QueryID)
				}
				lastMutation = mutation
				if sandboxGraphN(a.CreatedGeneration) > max {
					max = sandboxGraphN(a.CreatedGeneration)
					current = a
				}
				if b.CurrentQueryID == nil || *b.CurrentQueryID != a.QueryID {
					if len(sandboxGraphRelated[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", []string{"QueryID"}, a.QueryID)) != 1 {
						pointerOK = false
					}
				}
			}
			if len(children) == 0 {
				pointerOK = pointerOK && b.CurrentQueryID == nil
			} else {
				pointerOK = pointerOK && b.CurrentQueryID != nil && *b.CurrentQueryID == current.QueryID
			}
			if b.State == "retry_wait" {
				if b.CurrentQueryID == nil {
					if !sandboxGraphCode(b.LastErrorCode, "timeout") {
						g.bad("CommandStateRows", "CommandOutbox", b.ID)
					}
				} else if b.LastErrorCode == nil {
					trusted := false
					for _, snapshot := range sandboxGraphRelated[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", []string{"QueryID"}, *b.CurrentQueryID) {
						if _, ok := g.replies[snapshot.ID]; ok {
							trusted = true
						}
					}
					if !trusted {
						g.bad("CommandStateRows", "CommandOutbox", b.ID)
					}
				}
			}
			if b.State == "completed" {
				snapshots := sandboxGraphRelated[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", []string{"QueryID"}, sandboxGraphPtr(b.CurrentQueryID))
				pointerOK = pointerOK && len(snapshots) == 1
				if len(snapshots) == 1 {
					reply, ok := g.replies[snapshots[0].ID]
					pointerOK = pointerOK && ok && reply.CommandState == "completed"
				}
			}
			if !pointerOK {
				g.bad("QueryPointerRows", "CommandOutbox", b.ID)
			}
		} else if sandboxGraphHas(b.Kind, "checkout", "cancel") {
			supers := sandboxGraphRelated[SandboxGraphCommandOutboxRow](g, "CommandOutbox", []string{"OriginalOutboxID"}, b.ID)
			superCount := 0
			for _, s := range supers {
				if s.Kind == "query" {
					superCount++
				}
			}
			if b.State == "submission_unknown" && superCount != 1 {
				g.bad("CommandStateRows", "CommandOutbox", b.ID)
			}
			if b.State != "completed" {
				continue
			}
			witness := false
			for _, a := range sandboxGraphRelated[SandboxGraphObservationRow](g, "Observations", []string{"OutboxID", "Generation"}, b.ID, b.Generation) {
				if !g.tick() {
					return
				}
				r, ok := g.observations[a.ID]
				poll := r.CommandState == "in_progress" || r.Result != nil && r.Result.Status == "captured"
				if ok && (!poll || superCount == 1) {
					witness = true
				}
			}
			for _, s := range supers {
				for _, a := range sandboxGraphRelated[SandboxGraphQueryAttemptRow](g, "QueryAttempts", []string{"SupervisorID"}, s.ID) {
					if !g.tick() {
						return
					}
					if a.MutationGeneration != b.Generation || b.LastSubmissionUnknownAt == nil || a.CreatedAt.Before(*b.LastSubmissionUnknownAt) {
						continue
					}
					for _, v := range sandboxGraphRelated[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", []string{"QueryID"}, a.QueryID) {
						if r, ok := g.replies[v.ID]; ok && r.CommandState == "completed" {
							witness = true
						}
					}
				}
			}
			if !witness {
				g.bad("CommandStateRows", "CommandOutbox", b.ID)
			}
		}
	}
}
func (g *sandboxGraphData) evidence() {
	for _, e := range sandboxGraphRows[SandboxGraphEvidenceRow](g, "Evidence") {
		if !g.tick() {
			return
		}
		f := sandboxGraphFact{}
		originalOK := false
		if e.InboxID != nil && e.VerifiedQueryID == nil {
			n, ok := sandboxGraphGet[SandboxGraphInboxRow](g, "Inbox", *e.InboxID)
			if ok {
				v, vok := g.notification(n.CanonicalBody)
				if vok {
					h, _ := sandboxGraphInstant(v.OccurredAt)
					f = sandboxGraphFact{env: v.EnvironmentID, merchant: v.MerchantID, order: v.OrderID, kind: "unsupported", original: v.EventID, transaction: v.ProviderTransactionID, amount: &v.Amount, currency: &v.Currency, revision: &v.ProviderOrderRevision, happened: &h, received: n.ReceivedAt}
					if v.EventType == "capture.confirmed" {
						f.kind = "capture"
					} else if v.EventType == "checkout.failed" {
						f.kind = "observation"
						var v sandboxGraphNotificationFailure
						if g.strict(n.CanonicalBody, &v, 65536, 8, 128) && v.FailureCode == "order_closed" {
							f.kind = "closed"
						}
					}
					originalOK = n.BusinessDigest == sandboxGraphSHA(n.CanonicalBody) && g.keys[n.VerifiedKeyID] && n.EnvironmentID == v.EnvironmentID && n.MerchantID == v.MerchantID && n.EventID == v.EventID
				}
			}
		}
		if e.InboxID == nil && e.VerifiedQueryID != nil {
			n, ok := sandboxGraphGet[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries", *e.VerifiedQueryID)
			r, rok := g.replies[*e.VerifiedQueryID]
			if ok && rok {
				_, order, parsed := sandboxGraphMerchant(r.MerchantOrderID)
				f = sandboxGraphFact{env: r.EnvironmentID, merchant: r.MerchantID, order: order, kind: "observation", original: r.QueryID, received: n.ReceivedAt}
				originalOK = parsed
				if r.CommandState == "completed" && r.Result != nil {
					switch r.Result.Status {
					case "captured":
						c := r.Result.Capture
						if c != nil {
							h, _ := sandboxGraphInstant(c.OccurredAt)
							f.kind = "capture"
							f.transaction = &c.ProviderTransactionID
							f.amount = &c.Amount
							f.currency = &c.Currency
							f.revision = &c.ProviderOrderRevision
							f.happened = &h
						}
					case "closed":
						if r.Result.ClosedAt != nil {
							h, _ := sandboxGraphInstant(*r.Result.ClosedAt)
							f.kind = "closed"
							f.revision = r.ProviderOrderRevision
							f.happened = &h
						}
					}
				}
			}
		}
		if !originalOK {
			g.bad("EvidenceOriginalRows", "Evidence", e.ID)
		}
		matching := originalOK && e.EnvironmentID == f.env && e.MerchantID == f.merchant && e.OrderID == f.order && e.Kind == f.kind && sandboxGraphSame(e.ProviderTransactionID, f.transaction) && sandboxGraphSame(e.Amount, f.amount) && sandboxGraphSame(e.Currency, f.currency) && sandboxGraphSame(e.ProviderOrderRevision, f.revision) && e.VerifiedAt.Equal(f.received) && (e.HappenedAt == nil && f.happened == nil || e.HappenedAt != nil && f.happened != nil && e.HappenedAt.Equal(*f.happened)) && e.FactDigest == sandboxGraphFactDigest(f) && e.EnvironmentID == g.env.ID && e.MerchantID == g.env.MerchantID && (f.happened == nil || !f.happened.After(f.received.Add(5*time.Second)))
		if !matching {
			g.bad("EvidenceFactRows", "Evidence", e.ID)
		}
		f.valid = matching
		g.facts[e.ID] = f
		if _, ok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", e.ID); !ok {
			g.bad("ProcessingRows", "Evidence", e.ID)
		}
	}
	for _, n := range sandboxGraphRows[SandboxGraphInboxRow](g, "Inbox") {
		if !g.tick() {
			return
		}
		if len(sandboxGraphRelated[SandboxGraphEvidenceRow](g, "Evidence", []string{"InboxID"}, n.ID)) != 1 {
			g.bad("EvidenceOriginalRows", "Inbox", n.ID)
		}
	}
	for _, n := range sandboxGraphRows[SandboxGraphVerifiedQueryRow](g, "VerifiedQueries") {
		if !g.tick() {
			return
		}
		if len(sandboxGraphRelated[SandboxGraphEvidenceRow](g, "Evidence", []string{"VerifiedQueryID"}, n.ID)) != 1 {
			g.bad("EvidenceOriginalRows", "VerifiedQueries", n.ID)
		}
	}
	for _, p := range sandboxGraphRows[SandboxGraphProcessingRow](g, "Processing") {
		if !g.tick() {
			return
		}
		n := sandboxGraphN(p.AttemptCount)
		valid := g.exists("Evidence", p.EvidenceID) && sandboxGraphUint(p.Generation, 63, false) && p.Generation == p.AttemptCount && n <= 20 && ((p.State == "leased") == (p.LeaseUntil != nil))
		switch p.State {
		case "pending":
			valid = valid && n == 0 && p.LastErrorCode == nil
		case "leased":
			valid = valid && n > 0 && p.LastErrorCode == nil
		case "retry_wait":
			valid = valid && n > 0 && n < 20 && sandboxGraphCode(p.LastErrorCode, "temporary")
		case "processed":
			valid = valid && n > 0 && p.LastErrorCode == nil
		case "processing_review":
			valid = valid && n > 0 && (sandboxGraphCode(p.LastErrorCode, "permanent") || n == 20 && sandboxGraphCode(p.LastErrorCode, "attempts_exhausted"))
		default:
			valid = false
			g.bad("InvalidEnumRows", "Processing", p.EvidenceID)
		}
		if !valid {
			g.bad("ProcessingRows", "Processing", p.EvidenceID)
		}
	}
}

func sandboxGraphJSONAmount(raw, amount string) bool {
	v, ok := fundingGraphJSON(raw, []string{"amount"}, "")
	return ok && v["amount"].value == amount
}
