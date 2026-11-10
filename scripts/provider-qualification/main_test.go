package main

import (
	"testing"
	"time"
)

func TestQualificationEndpoint(t *testing.T) {
	if got, want := chatEndpoint("https://gateway.example/v1/"), "https://gateway.example/v1/chat/completions"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestQualificationRejectsInsecureOrMissingInputs(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		key   string
		model string
	}{
		{name: "missing key", base: "https://gateway.example/v1", model: "gpt-4o-mini"},
		{name: "insecure", base: "http://gateway.example/v1", key: "secret", model: "gpt-4o-mini"},
		{name: "query", base: "https://gateway.example/v1?x=1", key: "secret", model: "gpt-4o-mini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateInputs(tc.base, tc.key, tc.model, 8, time.Second); err == nil {
				t.Fatal("invalid qualification inputs were accepted")
			}
		})
	}
}

func TestQualificationBounds(t *testing.T) {
	if err := validateInputs("https://gateway.example/v1", "secret", "gpt-4o-mini", 129, time.Second); err == nil {
		t.Fatal("output bound above the qualification limit was accepted")
	}
	if err := validateInputs("https://gateway.example/v1", "secret", "gpt-4o-mini", 8, 11*time.Minute); err == nil {
		t.Fatal("qualification deadline above the limit was accepted")
	}
}
