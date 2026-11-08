package main

import "testing"

func TestValidateDatabaseAcceptsOnlyDedicated55439Database(t *testing.T) {
	valid := "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
	if err := validateDatabase(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"postgres://moni_test@127.0.0.1:5432/moni_test?sslmode=disable",
		"postgres://other@127.0.0.1:55439/moni_test?sslmode=disable",
		"postgres://moni_test@localhost:55439/moni_test?sslmode=disable",
		"postgres://moni_test@127.0.0.1:55439/postgres?sslmode=disable",
		"postgres://moni_test:secret@127.0.0.1:55439/moni_test?sslmode=disable",
	} {
		if err := validateDatabase(raw); err == nil {
			t.Fatalf("accepted disallowed database %q", raw)
		}
	}
}

func TestWithEnvReplacesExistingValuesDeterministically(t *testing.T) {
	got := withEnv([]string{"Z=old", "TEST_DATABASE_URL=5432", "A=one"}, map[string]string{"TEST_DATABASE_URL": "55439", "B": "two"})
	want := []string{"Z=old", "A=one", "B=two", "TEST_DATABASE_URL=55439"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSampleOrderContainsThreeBalancedPairs(t *testing.T) {
	want := []string{"before1", "after1", "after2", "before2", "before3", "after3"}
	if len(sampleOrder) != len(want) {
		t.Fatalf("sample order %v", sampleOrder)
	}
	for i := range want {
		if sampleOrder[i] != want[i] {
			t.Fatalf("sample order %v, want %v", sampleOrder, want)
		}
	}
}
