package fundinglotverify

import (
	"math/big"
	"sort"
	"time"
)

type procurementEvidenceConfig struct {
	id, label, provider, state, limit string
	created                           time.Time
	revision                          int64
}

func procurementEvidenceConfigEqual(a, b procurementEvidenceConfig) bool {
	return a.id == b.id && a.label == b.label && a.provider == b.provider && a.state == b.state && a.limit == b.limit && a.created.Equal(b.created)
}
func procurementEvidenceConfigSnapshot(s procurementCommandSnapshot) (procurementEvidenceConfig, bool) {
	if !s.valid || s.null {
		return procurementEvidenceConfig{}, false
	}
	v := func(k string) string { return procurementCommandSnapshotValue(s, k) }
	rev, ok := procurementEvidenceInt64(v("revision"))
	if !ok || rev.Sign() == 0 {
		return procurementEvidenceConfig{}, false
	}
	at, ok := procurementCommandTime(v("created_at"))
	if !ok {
		return procurementEvidenceConfig{}, false
	}
	return procurementEvidenceConfig{v("id"), v("label"), v("provider"), v("state"), v("limit_amount"), at, rev.Int64()}, true
}
func procurementEvidenceInt64(s string) (*big.Int, bool) {
	if len(s) > 19 {
		return nil, false
	}
	return procurementAttemptInt64(s)
}
func procurementEvidencePositiveInt64(s string) (*big.Int, bool) {
	n, ok := procurementEvidenceInt64(s)
	return n, ok && n.Sign() > 0
}

type procurementEvidenceAccountHistory struct {
	versions                  map[int64]procurementEvidenceConfig
	edges                     map[int64]uint64
	creates                   []procurementEvidenceConfig
	configurationBad, changed bool
}

func (x *procurementEvidenceIndex) accounts() error {
	w := x.w
	histories := make(map[string]*procurementEvidenceAccountHistory, len(x.accountRows))
	for id, r := range x.accountRows {
		if err := w.tick(); err != nil {
			return err
		}
		h := &procurementEvidenceAccountHistory{versions: make(map[int64]procurementEvidenceConfig), edges: make(map[int64]uint64)}
		histories[id] = h
		rev, revOK := procurementEvidenceInt64(r.Revision)
		limit, limitOK := procurementEvidenceInt64(r.LimitAmount)
		if !revOK || rev.Sign() == 0 || !limitOK || limit.Sign() < 0 || !procurementCommandMetadata(r.Label, 100, true) || !procurementCommandMetadata(r.Provider, 100, true) || !procurementAttemptStatus(r.State, "active", "paused") {
			h.configurationBad = true
			continue
		}
		h.versions[rev.Int64()] = procurementEvidenceConfig{id, r.Label, r.Provider, r.State, r.LimitAmount, r.CreatedAt, rev.Int64()}
	}
	add := func(h *procurementEvidenceAccountHistory, c procurementEvidenceConfig, current ProcurementEvidenceAccountRow) {
		rev, ok := procurementEvidenceInt64(current.Revision)
		if !ok || c.revision > rev.Int64() || c.id != current.ID || !c.created.Equal(current.CreatedAt) {
			h.configurationBad = true
		}
		if old, exists := h.versions[c.revision]; exists && !procurementEvidenceConfigEqual(old, c) {
			h.configurationBad = true
		} else {
			h.versions[c.revision] = c
		}
	}
	for _, command := range x.commands {
		if err := w.tick(); err != nil {
			return err
		}
		if command.Kind != "account_create" && command.Kind != "account_configure" {
			continue
		}
		current, exists := x.accountRows[command.TargetID]
		if !exists {
			continue
		}
		h := histories[current.ID]
		before, beforeOK := procurementEvidenceConfigSnapshot(procurementCommandDecode(command.Kind, command.Before))
		after, afterOK := procurementEvidenceConfigSnapshot(procurementCommandDecode(command.Kind, command.After))
		if command.Kind == "account_create" && command.Status == "applied" {
			if afterOK {
				h.creates = append(h.creates, after)
				add(h, after, current)
			} else {
				h.configurationBad = true
			}
		} else if command.Kind == "account_configure" && command.Status == "applied" {
			if beforeOK {
				add(h, before, current)
			}
			if afterOK {
				add(h, after, current)
			}
			if !beforeOK || !afterOK {
				h.configurationBad = true
				continue
			}
			if !procurementEvidenceConfigEqual(before, after) {
				h.changed = true
				if before.revision == after.revision-1 {
					h.edges[before.revision]++
					if h.edges[before.revision] > 1 {
						h.configurationBad = true
					}
				} else {
					h.configurationBad = true
				}
			}
		} else if command.Kind == "account_configure" && command.Status == "rejected" && command.Reason == "revision_conflict" && beforeOK {
			add(h, before, current)
		}
	}
	sourceCount := make(map[string]int)
	upperTotal := make(map[string]*big.Int)
	allReserved := make(map[string]bool)
	upperValid := make(map[string]bool)
	for _, p := range x.reservations {
		if err := w.tick(); err != nil {
			return err
		}
		sourceCount[p.AccountID]++
		if sourceCount[p.AccountID] == 1 {
			upperTotal[p.AccountID] = new(big.Int)
			allReserved[p.AccountID] = true
			upperValid[p.AccountID] = true
		}
		if n, ok := procurementBudgetUnsigned(p.UpperAmount); ok {
			upperTotal[p.AccountID].Add(upperTotal[p.AccountID], n)
		} else {
			allReserved[p.AccountID] = false
			upperValid[p.AccountID] = false
		}
		events := x.eventsByAttempt[p.AttemptID]
		if len(events) != 1 || events[0].Type != "reserved" {
			allReserved[p.AccountID] = false
		}
	}
	for id, current := range x.accountRows {
		if err := w.tick(); err != nil {
			return err
		}
		h := histories[id]
		createOK := len(h.creates) == 1
		var creation procurementEvidenceConfig
		if createOK {
			creation = h.creates[0]
			createOK = creation.revision == 1 && creation.id == id && creation.created.Equal(current.CreatedAt)
		}
		if !createOK {
			w.out.Mismatches.AccountCreationRows++
		}
		revisions := make([]int64, 0, len(h.versions))
		for revision := range h.versions {
			revisions = append(revisions, revision)
		}
		var sortErr error
		sort.Slice(revisions, func(i, j int) bool {
			if err := w.tick(); err != nil {
				sortErr = err
			}
			return revisions[i] < revisions[j]
		})
		if sortErr != nil {
			return sortErr
		}
		pauseGap := false
		if len(revisions) == 0 || revisions[0] != 1 {
			h.configurationBad = true
		}
		for i := 1; i < len(revisions); i++ {
			if err := w.tick(); err != nil {
				return err
			}
			before, after := h.versions[revisions[i-1]], h.versions[revisions[i]]
			if after.revision == before.revision+1 && h.edges[before.revision] == 1 {
				continue
			}
			automatic := after.revision == before.revision+1 && before.state == "active" && after.state == "paused" && before.label == after.label && before.provider == after.provider && before.limit == after.limit && before.id == after.id && before.created.Equal(after.created) && x.anomalyAccounts[id]
			if automatic {
				pauseGap = true
				h.changed = true
			} else {
				h.configurationBad = true
			}
		}
		if pauseGap {
			w.out.Unproven.AccountPauseOrderingUnproven++
		}
		if h.configurationBad {
			w.out.Mismatches.AccountConfigurationRows++
		}
		count := sourceCount[id]
		if count == 0 || !upperValid[id] {
			continue
		}
		constant := createOK && !h.configurationBad && !h.changed && !pauseGap && len(revisions) == 1 && creation.state == "active"
		provable := constant && (count == 1 || allReserved[id])
		if provable {
			limit, ok := procurementEvidenceInt64(creation.limit)
			if !ok || upperTotal[id].Cmp(limit) > 0 {
				w.out.Mismatches.AccountAdmissionRows++
			}
		} else {
			w.out.Unproven.AccountAdmissionOrderUnproven++
		}
	}
	return nil
}
