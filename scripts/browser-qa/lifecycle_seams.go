package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The run goroutine owns all child replacement and cleanup. Tests receive no
// arbitrary process launcher, environment secret, SQL hook or public endpoint.
type privateLifecycle struct {
	owner                                   *pgxpool.Pool
	fixture                                 *mailFixture
	origin, schema, appRole, projectionRole string
	binaryHash                              string
	killAndWait, restart                    func(context.Context) error
	proofMatches                            func(string, string) bool
}

func privateBinaryDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", stage("binary_hash_failed")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", stage("binary_hash_failed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func privateWaitReady(ctx context.Context, origin string, done <-chan error) (bool, error) {
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for {
		select {
		case <-ctx.Done():
			return false, stage("serve_readiness_timeout")
		case <-done:
			return true, stage("serve_exited_before_ready")
		default:
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/health/ready", nil)
		if err != nil {
			return false, stage("serve_readiness_timeout")
		}
		r, err := client.Do(req)
		ready := false
		if err == nil {
			var response struct {
				Status   string `json:"status"`
				Recovery bool   `json:"recovery_mode"`
			}
			err = json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&response)
			r.Body.Close()
			ready = r.StatusCode == 200 && err == nil && response.Status == "ready" && !response.Recovery
		}
		pause := 50 * time.Millisecond
		if ready {
			pause = 100 * time.Millisecond
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, stage("serve_readiness_timeout")
		case <-done:
			timer.Stop()
			return true, stage("serve_exited_before_ready")
		case <-timer.C:
		}
		if ready {
			return false, nil
		}
	}
}

func privateWaitRoleExit(ctx context.Context, pool *pgxpool.Pool, roles ...string) error {
	for {
		var count int
		if pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND usename=ANY($1::text[])`, roles).Scan(&count) != nil {
			return stage("mail_lifecycle_role_exit_unavailable")
		}
		if count == 0 {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return stage("mail_lifecycle_roles_still_connected")
		case <-timer.C:
		}
	}
}
