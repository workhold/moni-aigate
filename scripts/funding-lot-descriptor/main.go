package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	f := flag.NewFlagSet("funding-lot-descriptor", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	out := f.String("output", "", "new candidate directory")
	if f.Parse(os.Args[1:]) != nil || f.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "descriptor_generation_failed")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if e := run(ctx, *out, os.Getenv("C0_DESCRIPTOR_DATABASE_URL")); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("unreviewed_descriptor_candidate_exported metadata_only=true formal_expected=false cleanup=true")
}

func outputPath(s string) (string, error) {
	root, e := os.Getwd()
	if e != nil {
		return "", errStage
	}
	base := filepath.Join(root, ".local", "c0-descriptor-candidates")
	full, e := filepath.Abs(s)
	if e != nil || s == "" || filepath.Dir(full) != base {
		return "", errStage
	}
	name := filepath.Base(full)
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return "", errStage
	}
	for _, c := range name {
		if c != '-' && c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return "", errStage
		}
	}
	for _, p := range []string{filepath.Join(root, ".local"), base} {
		fi, e := os.Lstat(p)
		if e == nil && (!fi.IsDir() || fi.Mode()&os.ModeSymlink != 0) {
			return "", errStage
		}
		if e != nil && !os.IsNotExist(e) {
			return "", errStage
		}
	}
	if _, e = os.Lstat(full); !os.IsNotExist(e) {
		return "", errStage
	}
	return full, nil
}

func run(ctx context.Context, out, raw string) error {
	path, e := outputPath(out)
	if e != nil {
		return fmt.Errorf("output: %w", errStage)
	}
	in, e := loadInputs()
	if e != nil {
		return fmt.Errorf("inputs: %w", errStage)
	}
	cfg, e := databaseConfig(raw)
	if e != nil {
		return fmt.Errorf("database_configuration: %w", errStage)
	}
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return fmt.Errorf("database_open: %w", errStage)
	}
	defer admin.Close()
	version, e := verifyDatabase(ctx, admin)
	if e != nil {
		return fmt.Errorf("database_identity: %w", errStage)
	}
	ordinary, e := installProfile(ctx, admin, cfg, in, false)
	if e != nil {
		return fmt.Errorf("ordinary_profile: %w", e)
	}
	if ctx.Err() != nil {
		return errStage
	}
	sandbox, e := installProfile(ctx, admin, cfg, in, true)
	if e != nil {
		return fmt.Errorf("sandbox_profile: %w", e)
	}
	if !same(ordinary.base, sandbox.base) || !same(ordinary.after, sandbox.after) {
		return fmt.Errorf("profile_schema_equality: %w", errStage)
	}
	counts, e := checkShape(ordinary.base, ordinary.after)
	if e != nil {
		return fmt.Errorf("shape: %w", e)
	}
	delta, e := difference(ordinary.base, ordinary.after)
	if e != nil || checkSchemaDelta(delta) != nil {
		return fmt.Errorf("schema_delta: %w", errStage)
	}
	od, e := roleDifference(ordinary.baseRoles, ordinary.afterRoles)
	if e != nil {
		return fmt.Errorf("ordinary_role_delta: %w", errStage)
	}
	sd, e := roleDifference(sandbox.baseRoles, sandbox.afterRoles)
	if e != nil {
		return fmt.Errorf("sandbox_role_delta: %w", errStage)
	}
	current, e := loadInputs()
	if e != nil || !same(current.tools, in.tools) {
		return fmt.Errorf("source_changed: %w", errStage)
	}
	// No database operation follows: close the final administrative backend
	// before publishing evidence that all run-owned connections were removed.
	admin.Close()
	files := map[string]any{
		"provenance.json":  document{"format_version": 1, "artifact_kind": "unreviewed_descriptor_candidate", "server_version_num": version, "server_major": 16, "migrations": in.manifest, "manifest_sha256": manifestSHA, "ddl_sha256": ddlSHA, "acl_sha256": aclSHA, "provision_sources": provisionPins, "tool_sources": in.tools},
		"base-schema.json": ordinary.base, "c0-schema.json": ordinary.after, "c0-schema-delta.json": delta,
		"base-roles-ordinary.json": ordinary.baseRoles, "base-roles-sandbox.json": sandbox.baseRoles,
		"c0-roles-ordinary.json": ordinary.afterRoles, "c0-roles-sandbox.json": sandbox.afterRoles,
		"c0-role-deltas.json": document{"ordinary_app_acl": od, "sandbox_app_acl": sd},
	}
	encoded := map[string][]byte{}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var sums []fileDigest
	for _, n := range names {
		if ctx.Err() != nil {
			return errStage
		}
		b, e := canonical(files[n])
		if e != nil {
			return errStage
		}
		encoded[n] = b
		sums = append(sums, fileDigest{n, digest(b), len(b)})
	}
	summary := document{"format_version": 1, "artifact_kind": "unreviewed_descriptor_candidate", "files": sums, "counts": counts, "profile_checks": document{"same_base_structure": true, "same_c0_structure": true, "original_object_identity_preserved": true, "roles_provisioned_before_c0_only": true}, "cleanup": document{"profiles_removed": 2, "schemas_roles_connections_removed": true}, "metadata_only": true, "formal_expected": false, "epoch_issued": false, "runtime_proof_issued": false}
	b, e := canonical(summary)
	if e != nil {
		return errStage
	}
	encoded["summary.json"] = b
	names = append(names, "summary.json")
	// Installation and all cleanup have completed before an output is published.
	if ctx.Err() != nil {
		return errStage
	}
	if _, e := outputPath(out); e != nil {
		return errStage
	}
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return errStage
	}
	if os.Mkdir(path, 0700) != nil {
		return errStage
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(path)
		}
	}()
	for _, n := range names {
		if ctx.Err() != nil {
			return errStage
		}
		f, e := os.OpenFile(filepath.Join(path, n), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return errStage
		}
		_, we := f.Write(encoded[n])
		se := f.Sync()
		ce := f.Close()
		if we != nil || se != nil || ce != nil {
			return errStage
		}
	}
	if ctx.Err() != nil {
		return errStage
	}
	published = true
	return nil
}

func structureCounts(s document) document {
	tables, columns, support := 0, 0, 0
	for _, v := range s["relations"].([]any) {
		m := v.(object)
		id := m["identity"].(object)
		if id["scope"] == "automatic" {
			support++
			continue
		}
		if m["kind"] == "r" {
			tables++
			columns += len(m["attributes"].([]any))
		}
	}
	userTriggers := 0
	for _, v := range s["triggers"].([]any) {
		if !v.(object)["internal"].(bool) {
			userTriggers++
		}
	}
	indexes := 0
	for _, v := range s["indexes"].([]any) {
		if v.(object)["identity"].(object)["scope"] == "application" {
			indexes++
		}
	}
	return document{"tables": tables, "columns": columns, "functions": len(s["functions"].([]any)), "types": len(s["types"].([]any)), "user_triggers": userTriggers, "indexes": indexes, "constraints": len(s["constraints"].([]any)), "automatic_relations": support}
}

func checkShape(base, after document) (document, error) {
	for _, s := range []document{base, after} {
		for _, key := range []string{"namespace", "relations", "types", "functions"} {
			for _, v := range s[key].([]any) {
				m := v.(object)
				owner, ok := m["owner"].(object)
				if !ok || owner["class"] != "role" || owner["scope"] != "role" || owner["name"] != "application_owner" {
					return nil, errStage
				}
			}
		}
	}
	b, a := structureCounts(base), structureCounts(after)
	if b["tables"] != 109 || b["columns"] != 976 || a["tables"] != 125 || a["columns"] != 1145 {
		return nil, errStage
	}
	for _, s := range []document{base, after} {
		for _, v := range s["types"].([]any) {
			m := v.(object)
			if len(m["enum_labels"].([]any)) != 0 || m["range"] != nil || m["defined"] != true {
				return nil, errStage
			}
			if m["type"] == "c" {
				if m["category"] != "C" || m["relation"] == nil || m["element"] != nil || m["array"] == nil {
					return nil, errStage
				}
			} else if m["type"] == "b" {
				if m["category"] != "A" || m["relation"] != nil || m["element"] == nil || m["array"] != nil {
					return nil, errStage
				}
			} else {
				return nil, errStage
			}
		}
		for _, v := range s["functions"].([]any) {
			m := v.(object)
			if m["kind"] != "f" || m["aggregate"] != nil {
				return nil, errStage
			}
		}
		for _, v := range s["constraints"].([]any) {
			kind := v.(object)["kind"]
			if kind != "c" && kind != "f" && kind != "p" && kind != "u" && kind != "t" {
				return nil, errStage
			}
		}
	}
	for k, n := range map[string]int{"tables": 16, "columns": 169, "functions": 41, "types": 34, "user_triggers": 49, "indexes": 57, "constraints": 218} {
		if a[k].(int)-b[k].(int) != n {
			return nil, errStage
		}
	}
	for _, key := range []string{"operators", "casts", "collations", "conversions", "opclasses", "opfamilies", "policies", "rules", "extensions", "text_search", "statistics"} {
		if len(base[key].([]any)) != 0 || len(after[key].([]any)) != 0 {
			return nil, errStage
		}
	}
	return document{"base": b, "c0": a}, nil
}

func checkSchemaDelta(d document) error {
	removed := d["removed"].(document)
	changed := d["changed"].(document)
	for _, v := range removed {
		if a, ok := v.([]any); !ok || len(a) != 0 {
			return errStage
		}
	}
	for key, v := range changed {
		a, ok := v.([]any)
		if !ok {
			return errStage
		}
		if key != "relations" {
			if len(a) != 0 {
				return errStage
			}
			continue
		}
		if len(a) != 1 {
			return errStage
		}
		pair := a[0].(object)
		before, after := pair["before"].(object), pair["after"].(object)
		if before["identity"].(object)["name"] != "tenants" {
			return errStage
		}
		oldCols, newCols := before["attributes"].([]any), after["attributes"].([]any)
		if len(oldCols) != 8 || len(newCols) != 9 || !same(oldCols, newCols[:8]) {
			return errStage
		}
		last := newCols[8].(object)
		if !same(last["attnum"], 9) || !same(last["typmod"], -1) {
			return errStage
		}
		if last["name"] != "fl_created_xid" || last["not_null"] != false || last["default"] != nil || last["identity"] != "" || last["generated"] != "" || last["collation"] != nil {
			return errStage
		}
		if !same(last["type"], object{"namespace": "pg_catalog", "name": "xid8", "oid": 5069}) {
			return errStage
		}
		copyAfter := object{}
		for k, v := range after {
			copyAfter[k] = v
		}
		copyAfter["attributes"] = oldCols
		if !same(before, copyAfter) {
			return errStage
		}
	}
	return nil
}
