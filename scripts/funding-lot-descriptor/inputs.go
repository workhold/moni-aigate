package main

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/moni-ai/moni-aigate/migrations"
)

// Embedding the tool sources binds provenance to the actual built version.
//
//go:embed *.go
var toolSources embed.FS

const ddlName = "specs/022-self-service-payments/c0-production-schema.sql"
const aclName = "specs/022-self-service-payments/c0-production-permissions.sql"
const ddlSHA = "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047"
const aclSHA = "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae"
const manifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"

var provisionPins = []sourcePin{
	{"internal/operations/operations.go", "8ca51f8c3f5aee042fc28e8ede6fb450e4f0bff9650723eeb83b0560c00d9d79"},
	{"internal/operations/alert_roles.go", "3bdfd12de87c72d326813983d447fc5de82c97f8e3a85b00ee086801145e5073"},
	{"internal/operations/periodic_roles.go", "79ab556316fc2c366a97d897ab58c736c0e815de396f9904828b7abd65a4640d"},
	{"internal/operations/payment_roles.go", "966ad915a7ea277ed1ce13a76ca093342ad389166679cdfb8ac48b522907b8d2"},
	{"internal/operations/payment_roles_functions.go", "4307a70cc13890c4a2fe92d2306fd2efd68c75129808daac9309f6f368de9cb6"},
	{"migrations/embed.go", "915164219bb6490fc4570986a39a93696018f4d61e6465e0c1f6ec7532975b74"},
}

type inputs struct {
	ddl, acl        []byte
	manifest, tools []sourcePin
}

func loadInputs() (inputs, error) {
	var v inputs
	mod, e := os.ReadFile("go.mod")
	if e != nil || !strings.HasPrefix(string(mod), "module github.com/moni-ai/moni-aigate\n") {
		return v, errStage
	}
	v.ddl, e = os.ReadFile(ddlName)
	if e != nil || digest(v.ddl) != ddlSHA {
		return v, errStage
	}
	v.acl, e = os.ReadFile(aclName)
	if e != nil || digest(v.acl) != aclSHA {
		return v, errStage
	}
	names, e := fs.Glob(migrations.Files, "*.sql")
	if e != nil || len(names) != 22 {
		return v, errStage
	}
	sort.Strings(names)
	liveNames, e := filepath.Glob("migrations/*.sql")
	if e != nil || len(liveNames) != len(names) {
		return v, errStage
	}
	for i, n := range liveNames {
		if filepath.Base(n) != names[i] {
			return v, errStage
		}
	}
	for _, n := range names {
		b, e := migrations.Files.ReadFile(n)
		if e != nil {
			return v, errStage
		}
		live, e := os.ReadFile(filepath.Join("migrations", n))
		if e != nil || string(live) != string(b) {
			return v, errStage
		}
		v.manifest = append(v.manifest, sourcePin{n, digest(b)})
	}
	b, e := canonical(v.manifest)
	if e != nil || len(b) != 2459 || digest(b) != manifestSHA {
		return v, errStage
	}
	for _, p := range provisionPins {
		b, e := os.ReadFile(p.Name)
		if e != nil || digest(b) != p.SHA256 {
			return v, errStage
		}
	}
	names, e = fs.Glob(toolSources, "*.go")
	if e != nil {
		return v, errStage
	}
	sort.Strings(names)
	liveNames, e = filepath.Glob("scripts/funding-lot-descriptor/*.go")
	if e != nil || len(liveNames) != len(names) {
		return v, errStage
	}
	for i, n := range liveNames {
		if filepath.Base(n) != names[i] {
			return v, errStage
		}
	}
	for _, n := range names {
		b, e := toolSources.ReadFile(n)
		if e != nil {
			return v, errStage
		}
		live, e := os.ReadFile(filepath.Join("scripts/funding-lot-descriptor", n))
		if e != nil || string(live) != string(b) {
			return v, errStage
		}
		v.tools = append(v.tools, sourcePin{n, digest(b)})
	}
	return v, nil
}
