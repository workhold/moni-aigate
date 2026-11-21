package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

func runRouteReplay(ctx context.Context, pool *pgxpool.Pool, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("route-replay", flag.ContinueOnError)
	request := flags.String("request", "", "original request ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := uuid.Parse(*request); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("usage: moni-aigate route-replay --request <id>")
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,tenant_id,request_id,coalesce(attempt_id,''),sequence,policy_version_id,coalesce(selected_binding_id,''),coalesce(selected_physical_model_id,''),coalesce(draw::text,''),coalesce(sticky,''),degradation,algorithm_version,policy_snapshot,request_shape,candidates,encode(candidates_digest,'hex') FROM route_decisions WHERE request_id=$1 ORDER BY sequence`, *request)
	if err != nil {
		return err
	}
	defer rows.Close()
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	found := false
	for rows.Next() {
		found = true
		var decision routing.RouteDecision
		var policy, requestShape, candidates []byte
		if err = rows.Scan(&decision.ID, &decision.TenantID, &decision.RequestID, &decision.AttemptID, &decision.Sequence, &decision.PolicyVersionID, &decision.SelectedBindingID, &decision.SelectedPhysicalModelID, &decision.Draw, &decision.Sticky, &decision.Degradation, &decision.AlgorithmVersion, &policy, &requestShape, &candidates, &decision.CandidatesDigest); err != nil {
			return err
		}
		if json.Unmarshal(policy, &decision.Policy) != nil || json.Unmarshal(requestShape, &decision.Request) != nil || json.Unmarshal(candidates, &decision.Candidates) != nil {
			return fmt.Errorf("stored route decision is invalid")
		}
		replayed, err := routing.ReplayRoute(&decision)
		if err != nil {
			return err
		}
		if err = encoder.Encode(replayed); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("route decision not found")
	}
	return nil
}
