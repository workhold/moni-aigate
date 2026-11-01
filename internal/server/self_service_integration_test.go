package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func identityHTTPConfig(s *Server) {
	s.Config.Origin = "https://console.test"
	s.Config.Mail = config.MailConfig{Transport: "smtp", Host: "smtp.invalid", Port: 465, From: "identity@qa.invalid", TLSMode: "implicit"}
	s.Config.AuthPasswordResetEnabled = true
}
func identityHTTP(t *testing.T, s *Server, engine *gin.Engine, method, path, body, content, origin, remote string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = remote
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.AddCookie(&http.Cookie{Name: "moni_session", Value: "ignored-cookie-private-canary"})
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}
func TestSelfServiceHTTPCapabilityInitializationAndExistingLoginBoundary(t *testing.T) {
	s, pool := newAuthFixture(t)
	identityHTTPConfig(s)
	s.Config.AuthRegistrationEnabled = true
	engine := s.Engine()
	get := func(path string) *httptest.ResponseRecorder {
		return identityHTTP(t, s, engine, "GET", path, "", "", "", "192.0.2.1:1234")
	}
	var v struct {
		Data identityCapabilities `json:"data"`
	}
	w := get("/api/auth/capabilities")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Data.Registration.Reason != "setup_required" || !v.Data.PasswordReset.Available {
		t.Fatal("empty initialization not protected", w.Code, w.Body.String())
	}
	w = identityHTTP(t, s, engine, "POST", "/api/auth/registration/request", `{"email":"fresh@qa.invalid"}`, "application/json", s.Config.Origin, "192.0.2.2:1234")
	if w.Code != 503 {
		t.Fatal("registration before bootstrap was accepted", w.Code)
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled registration created user")
	}
	if err := s.Identity.Bootstrap(context.Background(), "bootstrap@qa.invalid", authTestPassword); err != nil {
		t.Fatal(err)
	}
	w = get("/api/auth/capabilities")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || !v.Data.Registration.Available {
		t.Fatal("bootstrap failed to enable configured registration", w.Code, w.Body.String())
	}
	if _, _, _, err := s.Identity.Login(context.Background(), "bootstrap@qa.invalid", authTestPassword, ""); err != nil {
		t.Fatal("legacy login changed", err)
	}
	s.Config.RecoveryMode = true
	w = get("/api/auth/capabilities")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Data.Registration.Reason != "recovery_mode" || v.Data.PasswordReset.Reason != "recovery_mode" {
		t.Fatal("recovery capability changed")
	}
	s.Guard = func(context.Context) error { return errors.New("private-guard-canary") }
	w = get("/api/auth/capabilities")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "instance_unavailable") || strings.Contains(w.Body.String(), "canary") {
		t.Fatal("capabilities bypassed ownership guard")
	}
}
func TestSelfServiceHTTPUniformEmailRequestsAndStrictAnonymousBoundary(t *testing.T) {
	s, pool := newAuthFixture(t)
	identityHTTPConfig(s)
	active, email := authUser(t, s, pool, "none")
	suspended, suspendedEmail := authUser(t, s, pool, "none")
	authExec(t, pool, "UPDATE users SET status='suspended' WHERE id=$1", suspended)
	engine := s.Engine()
	number := 0
	call := func(body, content, origin string) *httptest.ResponseRecorder {
		number++
		return identityHTTP(t, s, engine, "POST", "/api/auth/password-reset/request", body, content, origin, fmt.Sprintf("192.0.2.%d:1234", number))
	}
	for _, email := range []string{email, "absent@qa.invalid", suspendedEmail, email} {
		body, _ := json.Marshal(map[string]string{"email": email})
		w := call(string(body), "application/json", s.Config.Origin)
		if w.Code != 202 || w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("email request privacy changed", w.Code, w.Body.String())
		}
		raw := publicJSONKeys(t, w.Body.Bytes(), "data", "request_id")
		value := publicJSONKeys(t, raw["data"], "accepted")
		if string(value["accepted"]) != "true" {
			t.Fatal("email acceptance shape")
		}
	}
	var challenges, outboxes int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM identity_challenges),(SELECT count(*) FROM identity_mail_outbox)`).Scan(&challenges, &outboxes); err != nil || challenges != 1 || outboxes != 1 {
		t.Fatal("ineligible/cooldown created mail", challenges, outboxes, err)
	}
	var user string
	if err := pool.QueryRow(context.Background(), "SELECT user_id FROM identity_challenges").Scan(&user); err != nil || user != active {
		t.Fatal("reset proof mapped wrong account")
	}
	invalid := []string{`null`, `[]`, `{}`, `{"email":null}`, `{"email":3}`, `{"Email":"x@qa.invalid"}`, `{"email":"x@qa.invalid","email":"x@qa.invalid"}`, `{"email":"x@qa.invalid","password":"private-input-canary"}`, `{"email":"Display <x@qa.invalid>"}`, `{"email":"x@qa.invalid"}{}`, `{"email":"` + strings.Repeat("x", 4100) + `"}`, "{\"email\":\"\xff@qa.invalid\"}"}
	for _, body := range invalid {
		w := call(body, "application/json", s.Config.Origin)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") || strings.Contains(w.Body.String(), "canary") {
			t.Fatal("ambiguous identity request accepted or reflected input", w.Code)
		}
	}
	for _, content := range []string{"", "text/plain", "application/json; charset=latin1", "application/json; boundary=a"} {
		w := call(`{"email":"x@qa.invalid"}`, content, s.Config.Origin)
		if w.Code != 400 {
			t.Fatal("identity content type accepted", content, w.Code)
		}
	}
	if w := call(`{"email":"x@qa.invalid"}`, "application/json", "https://other.invalid"); w.Code != 403 {
		t.Fatal("identity origin bypass", w.Code)
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE"} {
		number++
		w := identityHTTP(t, s, engine, method, "/api/auth/password-reset/request", "{}", "application/json", s.Config.Origin, fmt.Sprintf("192.0.2.%d:1234", number))
		if w.Code != 404 {
			t.Fatal("unregistered identity method", method, w.Code)
		}
	}
}
func TestSelfServiceHTTPResetConsumesOnceAndRevokesSessionWithoutCookie(t *testing.T) {
	s, pool := newAuthFixture(t)
	identityHTTPConfig(s)
	user, email := authUser(t, s, pool, "none")
	rawSession, _ := authSession(t, s, pool, user, "", false, true)
	if err := s.Identity.RequestIdentityMail(context.Background(), identity.IdentityPurposePasswordReset, email, s.Config.Origin); err != nil {
		t.Fatal(err)
	}
	var id, cipher string
	if err := pool.QueryRow(context.Background(), "SELECT id::text,payload_ciphertext FROM identity_mail_outbox").Scan(&id, &cipher); err != nil {
		t.Fatal(err)
	}
	plain, err := s.Config.Secrets.OpenFor(cipher, "identity-mail-outbox", id)
	if err != nil {
		t.Fatal("fixture decrypt failed")
	}
	var payload struct {
		Link string `json:"link"`
	}
	if json.Unmarshal([]byte(plain), &payload) != nil {
		t.Fatal("fixture payload malformed")
	}
	token := strings.Split(payload.Link, "#token=")
	if len(token) != 2 {
		t.Fatal("fixture token missing")
	}
	engine := s.Engine()
	password := "reset-HTTP-fixture-password"
	body, _ := json.Marshal(map[string]string{"token": token[1], "password": password, "mfa_code": ""})
	for i := 0; i < 2; i++ {
		w := identityHTTP(t, s, engine, "POST", "/api/auth/password-reset/complete", string(body), "application/json", s.Config.Origin, fmt.Sprintf("198.51.100.%d:1234", i+1))
		if i == 0 {
			if w.Code != 200 || w.Header().Get("Set-Cookie") != "" {
				t.Fatal("reset did not complete without session issuance", w.Code)
			}
			publicJSONKeys(t, authData(t, w), "completed", "login_required")
		} else if w.Code != 400 || !strings.Contains(w.Body.String(), "identity_proof_invalid") {
			t.Fatal("used proof reset twice", w.Code)
		}
	}
	if _, err := s.Identity.Session(context.Background(), rawSession); !errors.Is(err, identity.ErrInvalid) {
		t.Fatal("old session survived password reset")
	}
	var hash string
	if err := pool.QueryRow(context.Background(), "SELECT password_hash FROM users WHERE id=$1", user).Scan(&hash); err != nil || !security.CheckPassword(hash, password) {
		t.Fatal("reset password not committed")
	}
}

func TestSelfServiceHTTPMalformedProofIsUniformAndDoesNotHashOrIssueSession(t *testing.T) {
	s, pool := newAuthFixture(t)
	identityHTTPConfig(s)
	s.Config.AuthRegistrationEnabled = true
	if err := s.Identity.Bootstrap(context.Background(), "bootstrap@qa.invalid", authTestPassword); err != nil {
		t.Fatal(err)
	}
	engine := s.Engine()
	n := 0
	for _, kind := range []string{"registration", "password-reset"} {
		for _, token := range []string{"", "private-token-canary", "ic1_" + security.Random(32), "ic1_" + strings.Repeat("_", 43)} {
			body := map[string]string{"token": token, "password": authTestPassword}
			if kind == "registration" {
				body["tenant_name"] = "proof QA"
			} else {
				body["mfa_code"] = ""
			}
			raw, _ := json.Marshal(body)
			n++
			w := identityHTTP(t, s, engine, "POST", "/api/auth/"+kind+"/complete", string(raw), "application/json", s.Config.Origin, fmt.Sprintf("203.0.113.%d:1234", n))
			if w.Code != 400 || !strings.Contains(w.Body.String(), "identity_proof_invalid") || strings.Contains(w.Body.String(), "canary") || w.Header().Get("Set-Cookie") != "" {
				t.Fatal("proof classification or privacy changed", kind, w.Code, w.Body.String())
			}
		}
	}
	var sessions, outboxes int
	if err := pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM sessions),(SELECT count(*) FROM identity_mail_outbox)`).Scan(&sessions, &outboxes); err != nil || sessions != 0 || outboxes != 0 {
		t.Fatal("invalid proof caused session/mail facts")
	}
	if s.identityHashes != 0 {
		t.Fatal("invalid proof leaked hash permit")
	}
}
