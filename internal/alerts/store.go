// Package alerts observes durable business facts and maintains in-app alert
// history. It has no provider, network, mail or financial write dependencies.
package alerts

import "github.com/jackc/pgx/v5/pgxpool"

type Store struct {
	pool    *pgxpool.Pool
	options RuntimeOptions
}

func NewStore(pool *pgxpool.Pool, options RuntimeOptions) (*Store, error) {
	if pool == nil {
		return nil, fail("alert_unavailable")
	}
	return &Store{pool: pool, options: options}, nil
}
