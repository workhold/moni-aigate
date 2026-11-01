package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestVoucherKeyringStrictConfiguration(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"v1":null}`, `{"v1":42}`, `{"v1":"short"}`,
		`{"v1":"` + key + `="}`, `{"v1":"` + key + `","v1":"` + key + `"}`,
		`{"bad.version":"` + key + `"}`, `{"v1":"` + key + `"} {}`,
		strings.Repeat(" ", 65537), string([]byte{0xff}),
	} {
		if _, err := ParseVoucherKeyring(raw, "v1"); !errors.Is(err, ErrVoucherKeyConfiguration) || err.Error() != "voucher_key_configuration" {
			t.Fatal("invalid configuration did not produce the fixed safe error")
		}
	}
	entries := make([]string, 129)
	for i := range entries {
		entries[i] = fmt.Sprintf(`"v%d":"%s"`, i, key)
	}
	if _, err := ParseVoucherKeyring("{"+strings.Join(entries, ",")+"}", "v0"); !errors.Is(err, ErrVoucherKeyConfiguration) {
		t.Fatal("accepted more than 128 versions")
	}
	if _, err := ParseVoucherKeyring(`{"v1":"`+key+`"}`, "missing"); !errors.Is(err, ErrVoucherKeyConfiguration) {
		t.Fatal("accepted missing active version")
	}
}

func TestVoucherKeyringHMACDomainsAndRotation(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key)
	k, err := ParseVoucherKeyring(`{"v1":"`+encoded+`","v2":"`+base64.RawURLEncoding.EncodeToString(make([]byte, 32))+`"}`, "v2")
	if err != nil {
		t.Fatal(err)
	}
	id := "f1028d33-6904-42dc-97ae-1caec982fc91"
	secret := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	code, err := k.CodeDigest("v1", id, secret)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("moni-aigate:voucher-code:v1\x00" + `{"voucher_id":"` + id + `","secret":"` + secret + `"}`))
	if code != hex.EncodeToString(mac.Sum(nil)) || len(code) != 64 {
		t.Fatal("code digest differs from frozen wire bytes")
	}
	fp, err := k.CommandFingerprint("v1", id, secret)
	if err != nil || fp == code {
		t.Fatal("HMAC domain separation failed")
	}
	old, _ := ParseVoucherKeyring(`{"v1":"`+encoded+`"}`, "v1")
	oldFP, _ := old.CommandFingerprint("v1", id, secret)
	if oldFP != fp || k.ActiveVersion() != "v2" || !k.HasVersion("v1") {
		t.Fatal("rotation changed a retained version")
	}
	if _, err = k.CodeDigest("missing", id, secret); !errors.Is(err, ErrVoucherKeyUnavailable) {
		t.Fatal("missing version did not fail safely")
	}
	for _, badID := range []string{strings.ToUpper(id), "00000000-0000-0000-0000-000000000000", strings.ReplaceAll(id, "-", "")} {
		if _, err = k.CodeDigest("v1", badID, secret); !errors.Is(err, ErrVoucherKeyConfiguration) {
			t.Fatal("accepted noncanonical voucher id")
		}
	}
	if _, err = k.CodeDigest("v1", id, secret+"="); !errors.Is(err, ErrVoucherKeyConfiguration) {
		t.Fatal("accepted padded secret")
	}
	var absent *VoucherKeyring
	if absent.ActiveVersion() != "" || absent.HasVersion("v1") {
		t.Fatal("nil keyring is usable")
	}
}
