package periodicbudget

import (
	"github.com/jackc/pgx/v5"
	"sort"
	"strings"
)

// ReadBoundedVerificationRows returns only untrusted local periodic-budget
// facts from the fixed guard and reading algorithm. It neither authenticates
// a maintenance lease nor issues a proof or complete-source authority.
// The ordinary Verifier keeps its original independent entry and no new guard.
func ReadBoundedVerificationRows(q VerificationQuery, schema string) (Verification, error) {
	if q == nil {
		return Verification{}, ErrVerifyInput
	}
	if schema == "" || schema == "pg_catalog" || strings.HasPrefix(schema, "pg_temp") || strings.IndexByte(schema, 0) >= 0 {
		return Verification{}, ErrVerifySchema
	}
	render := verifierRenderer(schema)
	if err := verifyMaintenanceResources(q, render); err != nil {
		return Verification{}, err
	}
	return readVerification(q, render)
}

const (
	maintenanceVerifierMaxRows       = 100000
	maintenanceVerifierMaxFieldBytes = 65536
	maintenanceVerifierMaxBytes      = 16 << 20
)

// This fixed preflight covers every declared field and every row in all 19
// relations, including the complete audit and migration tables. Only a scalar
// qualification leaves PostgreSQL. It is a maintenance compatibility boundary,
// not a bound on PostgreSQL aggregate/sort memory or the application's RSS.
var maintenanceVerifierResourcesSQL = maintenanceVerifierResourceSQL()

func maintenanceVerifierResourceSQL() string {
	aliases := make([]string, 0, len(verifierTables))
	for alias := range verifierTables {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	parts := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		var lengths, eligible []string
		for _, column := range strings.Fields(verifierColumns[alias]) {
			field := "source." + pgx.Identifier{column}.Sanitize()
			length := "coalesce(pg_catalog.octet_length(pg_catalog.convert_to(" + field + "::pg_catalog.text,'UTF8')),0)"
			lengths = append(lengths, length+"::pg_catalog.numeric")
			eligible = append(eligible, length+"<=65536")
		}
		parts = append(parts, "SELECT pg_catalog.count(*)::pg_catalog.numeric AS rows,coalesce(pg_catalog.sum("+strings.Join(lengths, "+")+"),0::pg_catalog.numeric) AS bytes,coalesce(pg_catalog.bool_and("+strings.Join(eligible, " AND ")+"),true) AS eligible FROM @"+alias+"@ AS source")
	}
	return "SELECT coalesce(pg_catalog.sum(rows),0::pg_catalog.numeric)<=100000 AND coalesce(pg_catalog.sum(bytes),0::pg_catalog.numeric)<=16777216 AND coalesce(pg_catalog.bool_and(eligible),true) FROM (" + strings.Join(parts, " UNION ALL ") + ") AS guarded"
}

func verifyMaintenanceResources(q verifierQuery, render verifierRender) error {
	if err := q.Check(); err != nil {
		return err
	}
	var eligible bool
	if err := q.QueryRow(render(maintenanceVerifierResourcesSQL)).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrVerifyExecution
	}
	return q.Check()
}
