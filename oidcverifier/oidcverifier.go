package oidcverifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/scotthaleen/go-app"
)

const (
	defaultMaxTokenBytes = 64 * 1024
	maxJWKSBytes         = 1024 * 1024
)

var supportedAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256,
	jose.RS384,
	jose.RS512,
	jose.ES256,
	jose.ES384,
	jose.ES512,
	jose.PS256,
	jose.PS384,
	jose.PS512,
	jose.EdDSA,
}

var (
	// ErrNotReady indicates that startup has not completed or shutdown has begun.
	ErrNotReady = errors.New("oidc verifier is not ready")
	// ErrAlreadyStarted indicates that Start was called more than once.
	ErrAlreadyStarted = errors.New("oidc verifier is already started")
	// ErrTokenTooLarge indicates that the token exceeds Config.MaxTokenBytes.
	ErrTokenTooLarge = errors.New("oidc token exceeds maximum size")
	// ErrInvalidToken indicates invalid token syntax, claims, signature, issuer, or expiry.
	ErrInvalidToken = errors.New("invalid oidc token")
	// ErrAudience indicates that no token audience is configured as accepted.
	ErrAudience = errors.New("oidc token audience is not allowed")
	// ErrAuthorizedParty indicates a missing or unaccepted azp claim.
	ErrAuthorizedParty = errors.New("oidc token authorized party is not allowed")
)

// Config configures an OIDC verifier.
type Config struct {
	// Name is the go-app component name. It defaults to "oidc-verifier".
	Name string
	// Issuer is the exact HTTPS OIDC issuer URL.
	Issuer string
	// Audiences contains accepted aud claim values. At least one is required.
	Audiences []string
	// AuthorizedParties contains accepted azp values. It defaults to Audiences.
	AuthorizedParties []string
	// SigningAlgorithms limits accepted algorithms. Discovery provides the default.
	SigningAlgorithms []string
	// MaxTokenBytes bounds raw JWT input. It defaults to 64 KiB.
	MaxTokenBytes int
}

type options struct {
	httpClient *http.Client
}

// Option customizes a Verifier.
type Option func(*options) error

// WithHTTPClient supplies the client used for discovery and JWKS retrieval.
// The caller retains ownership of the client and its transport.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) error {
		if client == nil {
			return errors.New("oidc verifier HTTP client cannot be nil")
		}
		opts.httpClient = client
		return nil
	}
}

// Verifier performs OIDC discovery and verifies ID tokens.
type Verifier struct {
	cfg               Config
	audiences         map[string]struct{}
	authorizedParties map[string]struct{}
	httpClient        *http.Client

	mu       sync.RWMutex
	started  bool
	stopped  bool
	verifier *oidc.IDTokenVerifier
}

// New validates static configuration without performing network requests.
func New(cfg Config, optionFns ...Option) (*Verifier, error) {
	if cfg.Name == "" {
		cfg.Name = "oidc-verifier"
	}
	if err := validateIssuer(cfg.Issuer); err != nil {
		return nil, err
	}

	audiences, err := valueSet("audience", cfg.Audiences, true)
	if err != nil {
		return nil, err
	}
	authorizedParties, err := valueSet("authorized party", cfg.AuthorizedParties, false)
	if err != nil {
		return nil, err
	}
	if len(authorizedParties) == 0 {
		authorizedParties = cloneSet(audiences)
	}
	if cfg.MaxTokenBytes < 0 {
		return nil, errors.New("oidc verifier maximum token size cannot be negative")
	}
	if cfg.MaxTokenBytes == 0 {
		cfg.MaxTokenBytes = defaultMaxTokenBytes
	}

	opts := options{httpClient: http.DefaultClient}
	for _, optionFn := range optionFns {
		if optionFn == nil {
			return nil, errors.New("oidc verifier option cannot be nil")
		}
		if err := optionFn(&opts); err != nil {
			return nil, err
		}
	}

	cfg.Audiences = slices.Clone(cfg.Audiences)
	cfg.AuthorizedParties = slices.Clone(cfg.AuthorizedParties)
	cfg.SigningAlgorithms = slices.Clone(cfg.SigningAlgorithms)
	return &Verifier{
		cfg:               cfg,
		audiences:         audiences,
		authorizedParties: authorizedParties,
		httpClient:        opts.httpClient,
	}, nil
}

func validateIssuer(issuer string) error {
	parsed, err := url.Parse(issuer)
	if err != nil {
		return fmt.Errorf("parse oidc issuer: %w", err)
	}
	if issuer == "" || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("oidc issuer must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("oidc issuer cannot contain user information, a query, or a fragment")
	}
	return nil
}

func valueSet(name string, values []string, required bool) (map[string]struct{}, error) {
	if required && len(values) == 0 {
		return nil, fmt.Errorf("oidc verifier requires at least one %s", name)
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("oidc verifier %s cannot be empty", name)
		}
		if _, exists := set[value]; exists {
			return nil, fmt.Errorf("oidc verifier %s %q is duplicated", name, value)
		}
		set[value] = struct{}{}
	}
	return set, nil
}

func cloneSet(source map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(source))
	for value := range source {
		clone[value] = struct{}{}
	}
	return clone
}

// Component returns the verifier's go-app lifecycle component.
func (v *Verifier) Component() *app.Component {
	return app.NewComponent(
		app.WithName(v.cfg.Name),
		app.WithOnStart(v.Start),
		app.WithOnStop(v.Stop),
	)
}

// Start performs OIDC discovery. A Verifier can only be started once.
func (v *Verifier) Start(ctx context.Context) error {
	v.mu.Lock()
	if v.started {
		v.mu.Unlock()
		return ErrAlreadyStarted
	}
	if v.stopped {
		v.mu.Unlock()
		return ErrNotReady
	}
	v.started = true
	v.mu.Unlock()

	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, v.httpClient), v.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("discover oidc provider: %w", err)
	}
	var metadata struct {
		JWKSURL    string   `json:"jwks_uri"`
		Algorithms []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return fmt.Errorf("decode oidc provider metadata: %w", err)
	}
	if err := validateJWKSURL(metadata.JWKSURL); err != nil {
		return err
	}
	algorithms := slices.Clone(v.cfg.SigningAlgorithms)
	if len(algorithms) == 0 {
		algorithms = filterSupportedAlgorithms(metadata.Algorithms)
	}
	verifier := oidc.NewVerifier(v.cfg.Issuer, &remoteKeySet{
		url:    metadata.JWKSURL,
		client: v.httpClient,
	}, &oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: algorithms,
	})

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped {
		return ErrNotReady
	}
	v.verifier = verifier
	return nil
}

// Stop makes the verifier unavailable. Calls already in progress may finish.
func (v *Verifier) Stop(context.Context) error {
	v.mu.Lock()
	v.stopped = true
	v.verifier = nil
	v.mu.Unlock()
	return nil
}

// Ready reports whether discovery completed and verification is available.
func (v *Verifier) Ready() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.verifier != nil
}

// Verify validates a raw ID token and returns its verified claims.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (*Token, error) {
	v.mu.RLock()
	verifier := v.verifier
	v.mu.RUnlock()
	if verifier == nil {
		return nil, ErrNotReady
	}
	if len(rawToken) > v.cfg.MaxTokenBytes {
		return nil, ErrTokenTooLarge
	}

	idToken, err := verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if idToken.Issuer != v.cfg.Issuer || idToken.Subject == "" || idToken.IssuedAt.IsZero() {
		return nil, ErrInvalidToken
	}
	if !matchesAny(idToken.Audience, v.audiences) {
		return nil, ErrAudience
	}

	var claims json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return nil, ErrInvalidToken
	}
	authorizedParty, present, err := readAuthorizedParty(claims)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if len(idToken.Audience) > 1 && !present {
		return nil, ErrAuthorizedParty
	}
	if present {
		if _, allowed := v.authorizedParties[authorizedParty]; !allowed {
			return nil, ErrAuthorizedParty
		}
	}

	return &Token{
		Issuer:          idToken.Issuer,
		Subject:         idToken.Subject,
		Audience:        slices.Clone(idToken.Audience),
		Expiry:          idToken.Expiry,
		IssuedAt:        idToken.IssuedAt,
		AuthorizedParty: authorizedParty,
		claims:          slices.Clone(claims),
	}, nil
}

func validateJWKSURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("oidc provider returned an invalid HTTPS JWKS URL")
	}
	return nil
}

func filterSupportedAlgorithms(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if slices.Contains(supportedAlgorithms, jose.SignatureAlgorithm(value)) {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

type remoteKeySet struct {
	url    string
	client *http.Client

	mu   sync.RWMutex
	keys []jose.JSONWebKey
}

func (r *remoteKeySet) VerifySignature(ctx context.Context, rawToken string) ([]byte, error) {
	signed, err := jose.ParseSigned(rawToken, supportedAlgorithms)
	if err != nil || len(signed.Signatures) != 1 {
		return nil, errors.New("invalid signed token")
	}

	r.mu.RLock()
	keys := slices.Clone(r.keys)
	r.mu.RUnlock()
	if payload, ok := verifyWithKeys(signed, keys); ok {
		return payload, nil
	}

	keys, err = r.fetch(ctx)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.keys = slices.Clone(keys)
	r.mu.Unlock()
	if payload, ok := verifyWithKeys(signed, keys); ok {
		return payload, nil
	}
	return nil, errors.New("no provider key verified the token")
}

func (r *remoteKeySet) fetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cache-Control", "no-cache")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxJWKSBytes {
		return nil, errors.New("JWKS response exceeds maximum size")
	}
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, err
	}
	return set.Keys, nil
}

func verifyWithKeys(signed *jose.JSONWebSignature, keys []jose.JSONWebKey) ([]byte, bool) {
	keyID := signed.Signatures[0].Header.KeyID
	for _, key := range keys {
		if keyID != "" && key.KeyID != keyID {
			continue
		}
		if payload, err := signed.Verify(key); err == nil {
			return payload, true
		}
	}
	return nil, false
}

func matchesAny(values []string, allowed map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := allowed[value]; ok {
			return true
		}
	}
	return false
}

func readAuthorizedParty(claims json.RawMessage) (string, bool, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(claims, &values); err != nil {
		return "", false, err
	}
	raw, present := values["azp"]
	if !present {
		return "", false, nil
	}
	var authorizedParty string
	if string(raw) == "null" || json.Unmarshal(raw, &authorizedParty) != nil {
		return "", true, errors.New("malformed authorized party")
	}
	return authorizedParty, true, nil
}

// Token contains standard fields and the verified provider claims.
type Token struct {
	Issuer   string
	Subject  string
	Audience []string
	Expiry   time.Time
	IssuedAt time.Time
	// AuthorizedParty is empty when the verified token omitted azp.
	AuthorizedParty string

	claims json.RawMessage
}

// Claims decodes the already verified payload into an application-owned value.
func (t *Token) Claims(target any) error {
	if t == nil {
		return errors.New("oidc token is nil")
	}
	return json.Unmarshal(t.claims, target)
}
