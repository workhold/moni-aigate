package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
)

func read(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func main() {
	hashes := map[string]string{
		"third_party/new-api/LICENSE":                 "8486a10c4393cee1c25392769ddd3b2d6c242d6ec7928e1414efff7dfb2f07ef",
		"third_party/new-api/NOTICE":                  "528067fcdf4f9d7e3fdb489d02cbdd36a0efa63fc2eb1686340612c26beb9f33",
		"third_party/new-api/THIRD-PARTY-LICENSES.md": "3848811f99c7b7ad802eea23775ccd262228c980f81eef64027f587689b0841d",
		"third_party/sub2api/LICENSE":                 "a5681bf9b05db14d86776930017c647ad9e6e56ff6bbcfdf21e5848288dfaf1b",
	}
	for path, expected := range hashes {
		body := read(path)
		if fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != expected {
			panic("upstream license content changed: " + path)
		}
	}
	contains := map[string][]string{
		"third_party/new-api/SOURCE.md": {
			"e449cc700eb569cdfc2199cb73e97de46944f43c", "Local upstream source modifications: **none**",
			"docs/verification/phase0-upstream-provenance.md", "https://github.com/QuantumNous/new-api",
		},
		"third_party/sub2api/SOURCE.md": {
			"10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13",
			"Local upstream source modifications: **none**", "docs/verification/phase0-upstream-provenance.md",
		},
		"deploy/external-upstreams.compose.yaml":          {"local/new-api:e449cc7", "weishaw/sub2api@sha256:868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13", "new-api-db", "sub2api-db"},
		"docs/verification/phase0-external-upstream.md":   {"e449cc700eb569cdfc2199cb73e97de46944f43c", "10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "supplier_authenticated\":false"},
		"third_party/new-api/NOTICE":                      {"https://github.com/QuantumNous/new-api", "Frontend design and development by New API contributors.", "THIRD-PARTY-LICENSES.md"},
		"docs/verification/phase0-upstream-provenance.md": {"e449cc700eb569cdfc2199cb73e97de46944f43c", "10ba8f9b4dc2e6023d4a9879ab77b21841babd10", "upstream-provenance-check"},
	}
	for path, needles := range contains {
		body := read(path)
		for _, needle := range needles {
			if !strings.Contains(body, needle) {
				panic("upstream provenance requirement missing in " + path + ": " + needle)
			}
		}
	}
	fmt.Println("PASS upstream provenance: exact New API/Sub2API commits, LICENSE/NOTICE files, source modification records, and Compose image identity")
}
