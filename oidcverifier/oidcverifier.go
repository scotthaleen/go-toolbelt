package oidcverifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/scotthaleen/go-app"
	"github.com/scotthaleen/go-toolbelt/strictjson"
)

const (
	defaultMaxTokenBytes     = 64 * 1024
	maxProviderMetadataBytes = 1024 * 1024
	maxJWKSBytes             = 1024 * 1024
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

// ProviderMetadata contains the validated OIDC endpoints needed by clients.
type ProviderMetadata struct {
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	JWKSURL               string
	ResponseTypes         []string
	CodeChallengeMethods  []string
	SigningAlgorithms     []string
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
	metadata ProviderMetadata
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

// Discover retrieves bounded provider metadata and validates its issuer and
// HTTPS endpoints. The caller retains ownership of client and its transport.
func Discover(ctx context.Context, issuer string, client *http.Client) (ProviderMetadata, error) {
	if err := validateIssuer(issuer); err != nil {
		return ProviderMetadata{}, err
	}
	if client == nil {
		return ProviderMetadata{}, errors.New("oidc discovery HTTP client cannot be nil")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return ProviderMetadata{}, fmt.Errorf("create oidc discovery request: %w", err)
	}
	discoveryClient := httpsOnlyClient(client)
	response, err := discoveryClient.Do(request)
	if err != nil {
		return ProviderMetadata{}, fmt.Errorf("request oidc provider metadata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ProviderMetadata{}, fmt.Errorf("oidc discovery endpoint returned %s", response.Status)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ProviderMetadata{}, errors.New("oidc discovery endpoint returned a non-JSON content type")
	}
	var document struct {
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		JWKSURL               string   `json:"jwks_uri"`
		ResponseTypes         []string `json:"response_types_supported"`
		SubjectTypes          []string `json:"subject_types_supported"`
		CodeChallengeMethods  []string `json:"code_challenge_methods_supported"`
		SigningAlgorithms     []string `json:"id_token_signing_alg_values_supported"`
	}
	if err := strictjson.DecodeReader(response.Body, maxProviderMetadataBytes, &document); err != nil {
		return ProviderMetadata{}, fmt.Errorf("decode oidc provider metadata: %w", err)
	}
	if document.Issuer != issuer {
		return ProviderMetadata{}, &oidc.IssuerMismatchError{Provided: issuer, Discovered: document.Issuer}
	}
	if err := validateEndpoint("authorization", document.AuthorizationEndpoint); err != nil {
		return ProviderMetadata{}, err
	}
	if document.TokenEndpoint != "" {
		if err := validateEndpoint("token", document.TokenEndpoint); err != nil {
			return ProviderMetadata{}, err
		}
	}
	if len(document.ResponseTypes) == 0 {
		return ProviderMetadata{}, errors.New("oidc provider returned no supported response type")
	}
	if supportsCodeFlow(document.ResponseTypes) && document.TokenEndpoint == "" {
		return ProviderMetadata{}, errors.New("oidc provider omitted the token endpoint required for code flow")
	}
	if len(document.SubjectTypes) == 0 {
		return ProviderMetadata{}, errors.New("oidc provider returned no supported subject type")
	}
	if err := validateStringValues("response type", document.ResponseTypes); err != nil {
		return ProviderMetadata{}, err
	}
	if err := validateStringValues("subject type", document.SubjectTypes); err != nil {
		return ProviderMetadata{}, err
	}
	if err := validateStringValues("code challenge method", document.CodeChallengeMethods); err != nil {
		return ProviderMetadata{}, err
	}
	if err := validateEndpoint("JWKS", document.JWKSURL); err != nil {
		return ProviderMetadata{}, err
	}
	if err := validateStringValues("ID-token signing algorithm", document.SigningAlgorithms); err != nil {
		return ProviderMetadata{}, err
	}
	if !slices.Contains(document.SigningAlgorithms, string(jose.RS256)) {
		return ProviderMetadata{}, errors.New("oidc provider does not support required RS256 ID-token signing")
	}
	algorithms := filterSupportedAlgorithms(document.SigningAlgorithms)
	if len(algorithms) == 0 {
		return ProviderMetadata{}, errors.New("oidc provider returned no supported ID-token signing algorithm")
	}
	return ProviderMetadata{
		Issuer:                document.Issuer,
		AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint:         document.TokenEndpoint,
		JWKSURL:               document.JWKSURL,
		ResponseTypes:         slices.Clone(document.ResponseTypes),
		CodeChallengeMethods:  slices.Clone(document.CodeChallengeMethods),
		SigningAlgorithms:     algorithms,
	}, nil
}

func httpsOnlyClient(client *http.Client) *http.Client {
	clone := *client
	checkRedirect := client.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("oidc endpoint redirect must use HTTPS")
		}
		if checkRedirect != nil {
			return checkRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &clone
}

func supportsCodeFlow(responseTypes []string) bool {
	for _, responseType := range responseTypes {
		if slices.Contains(strings.Fields(responseType), "code") {
			return true
		}
	}
	return false
}

func validateStringValues(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("oidc provider returned an empty %s", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("oidc provider returned a duplicate %s", name)
		}
		seen[value] = struct{}{}
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

	metadata, err := Discover(ctx, v.cfg.Issuer, v.httpClient)
	if err != nil {
		return fmt.Errorf("discover oidc provider: %w", err)
	}
	algorithms := slices.Clone(v.cfg.SigningAlgorithms)
	if len(algorithms) == 0 {
		algorithms = slices.Clone(metadata.SigningAlgorithms)
	}
	verifier := oidc.NewVerifier(v.cfg.Issuer, &remoteKeySet{
		url:    metadata.JWKSURL,
		client: httpsOnlyClient(v.httpClient),
	}, &oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: algorithms,
	})

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stopped {
		return ErrNotReady
	}
	v.metadata = cloneMetadata(metadata)
	v.verifier = verifier
	return nil
}

// Stop makes the verifier unavailable. Calls already in progress may finish.
func (v *Verifier) Stop(context.Context) error {
	v.mu.Lock()
	v.stopped = true
	v.metadata = ProviderMetadata{}
	v.verifier = nil
	v.mu.Unlock()
	return nil
}

// Metadata returns validated discovery metadata after startup.
func (v *Verifier) Metadata() (ProviderMetadata, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.verifier == nil {
		return ProviderMetadata{}, ErrNotReady
	}
	return cloneMetadata(v.metadata), nil
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

func validateEndpoint(name, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("oidc provider returned an invalid HTTPS %s endpoint", name)
	}
	return nil
}

func cloneMetadata(metadata ProviderMetadata) ProviderMetadata {
	metadata.ResponseTypes = slices.Clone(metadata.ResponseTypes)
	metadata.CodeChallengeMethods = slices.Clone(metadata.CodeChallengeMethods)
	metadata.SigningAlgorithms = slices.Clone(metadata.SigningAlgorithms)
	return metadata
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
