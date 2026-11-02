package billing

// These fixed query/row seams are intentionally narrow. They let the
// maintenance integrator run the one approved reader through a real ReadLease
// without making billing depend on maintenance authority.
type fundingSourceQuery interface {
	Query(string, ...any) (fundingSourceRows, error)
}

type fundingSourceRows interface {
	Next() (bool, error)
	Scan(...any) error
	Err() error
	Close() error
}
