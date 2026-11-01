package billing

import "testing"

func TestPostgresConcurrentDuplicateAttemptsKeepReservationLock(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	in := AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
	type output struct {
		attempt Attempt
		err     error
	}
	done := make(chan output, 2)
	for range 2 {
		go func() {
			a, err := f.store.RecordAttempt(f.ctx, in)
			done <- output{a, err}
		}()
	}
	a, b := <-done, <-done
	if a.err != nil || b.err != nil || a.attempt != b.attempt {
		t.Fatal("duplicate attempts escaped reservation serialization", a, b)
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE request_id=$1", r.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	f.wallet(t, 880, 120)
	f.check(t)
}
