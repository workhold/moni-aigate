# Independent initialization golden

`mixed_golden.json` is a static hand-calculated three-tenant INSERT draft. It was assembled without running `Build` or any verifier. UUIDs were calculated separately using Python standard-library SHA-1 over the frozen epoch namespace and U64BE UTF-8 length frames, then frozen as literal JSON values. The external Go tests also assert literal UUID vectors.

Let M = 9223372036854775807. Tenant A receives 100 + 70 + 50, finishes an old reservation of 60 with charge 30, receives approved refunds 10 + 5, and keeps unfinished reservations of 40 and 0. A = 165, H = 40; pending refund 7 has no effect. Tenant B receives (M−100) + (M−70), keeps H = M−20, and therefore A = M−150. Its A+H = 18446744073709551444 exceeds BIGINT while each bucket remains valid. The third tenant has a legal empty old ID and no wallet accounts or history.

The global totals are A = M+15 and H = M+20. Shared platform balances are funding clearing −M, grant clearing −M, sandbox clearing −50, and revenue 15; each fits signed BIGINT and all journal lines balance.

Independent counts: T=3, F=5, B=1, V=4, S=1, Q=2, U=3, a=2, h=2, L=4. Thus G=3T+U+2F+B+V+2S+2Q+3L+h+2=49 and DataRows=47. Four positive openings create four lots/events/caches; only two positive held openings create allocations. Old terminal/approved/sandbox facts are membership, not duplicated issuance.
