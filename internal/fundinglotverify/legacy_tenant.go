package fundinglotverify

// Legacy tenant primary and foreign keys inherit PostgreSQL TEXT identity:
// empty is an identity; only a nil nullable pointer denotes a platform scope.
// Other object IDs and business references keep their original validators.
func legacyTenantText(value string) bool { return ledgerText(value) }
