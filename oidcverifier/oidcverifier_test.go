package oidcverifier

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scotthaleen/go-app"
)

func TestNewValidatesConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  Config
		opts []Option
	}{
		{name: "empty issuer", cfg: Config{Audiences: []string{"client"}}},
		{name: "HTTP issuer", cfg: Config{Issuer: "http://issuer.example", Audiences: []string{"client"}}},
		{name: "issuer user", cfg: Config{Issuer: "https://user@issuer.example", Audiences: []string{"client"}}},
		{name: "issuer query", cfg: Config{Issuer: "https://issuer.example?x=1", Audiences: []string{"client"}}},
		{name: "issuer fragment", cfg: Config{Issuer: "https://issuer.example#x", Audiences: []string{"client"}}},
		{name: "no audiences", cfg: Config{Issuer: "https://issuer.example"}},
		{name: "empty audience", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{""}}},
		{name: "duplicate audience", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a", "a"}}},
		{name: "empty authorized party", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a"}, AuthorizedParties: []string{""}}},
		{name: "duplicate authorized party", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a"}, AuthorizedParties: []string{"a", "a"}}},
		{name: "negative token size", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a"}, MaxTokenBytes: -1}},
		{name: "nil HTTP client", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a"}}, opts: []Option{WithHTTPClient(nil)}},
		{name: "nil option", cfg: Config{Issuer: "https://issuer.example", Audiences: []string{"a"}}, opts: []Option{nil}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(test.cfg, test.opts...); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestVerifierLifecycle(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.verifier(t, Config{Audiences: []string{"client"}})
	if verifier.Ready() {
		t.Fatal("Ready() = true before Start")
	}
	if _, err := verifier.Verify(context.Background(), "token"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Verify() error = %v, want ErrNotReady", err)
	}
	if got := verifier.Component().Name(); got != "oidc-verifier" {
		t.Fatalf("Component().Name() = %q", got)
	}

	if err := verifier.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !verifier.Ready() {
		t.Fatal("Ready() = false after Start")
	}
	if err := verifier.Start(context.Background()); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start() error = %v, want ErrAlreadyStarted", err)
	}
	if err := verifier.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := verifier.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if verifier.Ready() {
		t.Fatal("Ready() = true after Stop")
	}
	if _, err := verifier.Verify(context.Background(), "token"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Verify() after Stop error = %v, want ErrNotReady", err)
	}
}

func TestComponentStartsAndStopsInApplicationOrder(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.verifier(t, Config{Name: "company-oidc", Audiences: []string{"client"}})
	observedReadyDuringDependentStop := false
	dependent := app.NewComponent(
		app.WithName("dependent"),
		app.WithOnStop(func(context.Context) error {
			observedReadyDuringDependentStop = verifier.Ready()
			return nil
		}),
	)
	application := app.New(
		context.Background(),
		app.WithSignalHandling(false),
		app.WithSequentialStartup(app.Registered(verifier), app.Managed(dependent)),
	)
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("App.Start() error = %v", err)
	}
	if !verifier.Ready() {
		t.Fatal("Ready() = false after application startup")
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("App.Stop() error = %v", err)
	}
	if !observedReadyDuringDependentStop {
		t.Fatal("dependent stopped after verifier")
	}
	if verifier.Ready() {
		t.Fatal("Ready() = true after application shutdown")
	}
}

func TestStartHonorsDiscoveryContextAndRejectsMalformedDiscovery(t *testing.T) {
	t.Run("canceled", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		verifier, err := New(Config{Issuer: server.URL, Audiences: []string{"client"}}, WithHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := verifier.Start(ctx); err == nil {
			t.Fatal("Start() error = nil")
		}
		if verifier.Ready() {
			t.Fatal("Ready() = true after canceled discovery")
		}
	})

	t.Run("malformed", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{"))
		}))
		defer server.Close()
		verifier, err := New(Config{Issuer: server.URL, Audiences: []string{"client"}}, WithHTTPClient(server.Client()))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if err := verifier.Start(context.Background()); err == nil {
			t.Fatal("Start() error = nil")
		}
	})
}

func TestVerifyCancelsJWKSRequest(t *testing.T) {
	key := newRSAKey(t)
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                server.URL,
				"jwks_uri":                              server.URL + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			requestStarted <- struct{}{}
			<-r.Context().Done()
			requestCanceled <- struct{}{}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	verifier, err := New(Config{Issuer: server.URL, Audiences: []string{"client"}}, WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := verifier.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	raw := signToken(t, key, "key-1", map[string]any{
		"iss": server.URL,
		"sub": "subject-1",
		"aud": "client",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := verifier.Verify(ctx, raw)
		done <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("JWKS request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Verify() did not return after cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("JWKS HTTP request was not canceled")
	}
}

func TestVerifyAudienceAndAuthorizedParty(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.startedVerifier(t, Config{Audiences: []string{"desktop", "web"}})
	tests := []struct {
		name    string
		claims  map[string]any
		wantErr error
	}{
		{name: "first audience", claims: map[string]any{"aud": "desktop"}},
		{name: "second audience", claims: map[string]any{"aud": "web"}},
		{name: "single audience with azp", claims: map[string]any{"aud": "desktop", "azp": "desktop"}},
		{name: "multiple audiences", claims: map[string]any{"aud": []string{"other", "web"}, "azp": "web"}},
		{name: "audience rejected", claims: map[string]any{"aud": "other"}, wantErr: ErrAudience},
		{name: "multiple audiences need azp", claims: map[string]any{"aud": []string{"desktop", "other"}}, wantErr: ErrAuthorizedParty},
		{name: "azp rejected", claims: map[string]any{"aud": "desktop", "azp": "other"}, wantErr: ErrAuthorizedParty},
		{name: "malformed azp", claims: map[string]any{"aud": "desktop", "azp": 42}, wantErr: ErrInvalidToken},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := issuer.token(t, test.claims)
			token, err := verifier.Verify(context.Background(), raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Verify() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil && token.Subject != "subject-1" {
				t.Fatalf("Token.Subject = %q", token.Subject)
			}
		})
	}

	explicitPartyVerifier := issuer.startedVerifier(t, Config{
		Audiences:         []string{"api"},
		AuthorizedParties: []string{"desktop"},
	})
	raw := issuer.token(t, map[string]any{"aud": "api", "azp": "desktop"})
	if _, err := explicitPartyVerifier.Verify(context.Background(), raw); err != nil {
		t.Fatalf("Verify() with explicit authorized party error = %v", err)
	}
}

func TestVerifyStandardClaimsAndCustomClaims(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.startedVerifier(t, Config{Audiences: []string{"client"}})
	raw := issuer.token(t, map[string]any{"aud": "client", "email": "person@example.com", "admin": true})
	token, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	var claims struct {
		Email string `json:"email"`
		Admin bool   `json:"admin"`
	}
	if err := token.Claims(&claims); err != nil {
		t.Fatalf("Claims() error = %v", err)
	}
	if claims.Email != "person@example.com" || !claims.Admin {
		t.Fatalf("Claims() = %+v", claims)
	}
	if len(token.Audience) != 1 || token.Audience[0] != "client" || token.Issuer != issuer.server.URL {
		t.Fatalf("Token = %+v", token)
	}

	tests := []struct {
		name   string
		claims map[string]any
		key    *rsa.PrivateKey
	}{
		{name: "wrong issuer", claims: map[string]any{"iss": "https://other.example", "aud": "client"}},
		{name: "expired", claims: map[string]any{"aud": "client", "exp": time.Now().Add(-time.Hour).Unix()}},
		{name: "malformed audience", claims: map[string]any{"aud": 42}},
		{name: "missing subject", claims: map[string]any{"aud": "client", "sub": ""}},
		{name: "missing issued at", claims: map[string]any{"aud": "client", "iat": nil}},
		{name: "invalid signature", claims: map[string]any{"aud": "client"}, key: newRSAKey(t)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawToken := issuer.tokenWithKey(t, test.claims, test.key)
			_, err := verifier.Verify(context.Background(), rawToken)
			if !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
			}
			if strings.Contains(fmt.Sprint(err), rawToken) {
				t.Fatal("Verify() error contains raw token")
			}
		})
	}
}

func TestVerifyLimitsSigningAlgorithmsAndTokenSize(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.startedVerifier(t, Config{Audiences: []string{"client"}, MaxTokenBytes: 8})
	if _, err := verifier.Verify(context.Background(), "123456789"); !errors.Is(err, ErrTokenTooLarge) {
		t.Fatalf("Verify() error = %v, want ErrTokenTooLarge", err)
	}

	algorithmVerifier := issuer.startedVerifier(t, Config{
		Audiences:         []string{"client"},
		SigningAlgorithms: []string{"ES256"},
	})
	if _, err := algorithmVerifier.Verify(context.Background(), issuer.token(t, map[string]any{"aud": "client"})); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("Verify() error = %v, want ErrInvalidToken", err)
	}
}

func TestVerifyRefreshesJWKSAfterRotation(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.startedVerifier(t, Config{Audiences: []string{"client"}})
	if _, err := verifier.Verify(context.Background(), issuer.token(t, map[string]any{"aud": "client"})); err != nil {
		t.Fatalf("Verify() before rotation error = %v", err)
	}
	issuer.rotate(t)
	if _, err := verifier.Verify(context.Background(), issuer.token(t, map[string]any{"aud": "client"})); err != nil {
		t.Fatalf("Verify() after rotation error = %v", err)
	}
}

func TestVerifyIsConcurrent(t *testing.T) {
	issuer := newTestIssuer(t)
	verifier := issuer.startedVerifier(t, Config{Audiences: []string{"client"}})
	raw := issuer.token(t, map[string]any{"aud": "client"})

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := verifier.Verify(context.Background(), raw)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Verify() error = %v", err)
		}
	}
}

type testIssuer struct {
	server *httptest.Server
	mu     sync.RWMutex
	key    *rsa.PrivateKey
	kid    string
}

func newTestIssuer(t *testing.T) *testIssuer {
	t.Helper()
	issuer := &testIssuer{key: newRSAKey(t), kid: "key-1"}
	issuer.server = httptest.NewTLSServer(http.HandlerFunc(issuer.serveHTTP))
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (i *testIssuer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                i.server.URL,
			"authorization_endpoint":                i.server.URL + "/authorize",
			"token_endpoint":                        i.server.URL + "/token",
			"jwks_uri":                              i.server.URL + "/keys",
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case "/keys":
		i.mu.RLock()
		key, kid := &i.key.PublicKey, i.kid
		i.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk(key, kid)}})
	default:
		http.NotFound(w, r)
	}
}

func (i *testIssuer) verifier(t *testing.T, cfg Config) *Verifier {
	t.Helper()
	cfg.Issuer = i.server.URL
	verifier, err := New(cfg, WithHTTPClient(i.server.Client()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return verifier
}

func (i *testIssuer) startedVerifier(t *testing.T, cfg Config) *Verifier {
	t.Helper()
	verifier := i.verifier(t, cfg)
	if err := verifier.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return verifier
}

func (i *testIssuer) token(t *testing.T, overrides map[string]any) string {
	t.Helper()
	return i.tokenWithKey(t, overrides, nil)
}

func (i *testIssuer) tokenWithKey(t *testing.T, overrides map[string]any, signingKey *rsa.PrivateKey) string {
	t.Helper()
	i.mu.RLock()
	key, kid := i.key, i.kid
	i.mu.RUnlock()
	if signingKey != nil {
		key = signingKey
	}
	claims := map[string]any{
		"iss": i.server.URL,
		"sub": "subject-1",
		"aud": "client",
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Add(-time.Minute).Unix(),
	}
	for name, value := range overrides {
		claims[name] = value
	}
	return signToken(t, key, kid, claims)
}

func (i *testIssuer) rotate(t *testing.T) {
	t.Helper()
	i.mu.Lock()
	i.key = newRSAKey(t)
	i.kid = "key-2"
	i.mu.Unlock()
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func jwk(key *rsa.PublicKey, kid string) map[string]any {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
	}
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT claims: %v", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(encoded))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature)
}
