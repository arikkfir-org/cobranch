// Package oidc speaks OpenID Connect's authorization code flow, with PKCE and a nonce, to the identity providers an
// installation trusts: auth.Provider over github.com/coreos/go-oidc. Google is a preset; any provider that publishes
// its discovery document works.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"cobranch.dev/internal/services/auth"
)

// Presets are the providers the values may name instead of an issuer.
var Presets = map[string]string{
	"google": "https://accounts.google.com",
}

// Config is one identity provider, as the values describe it.
type Config struct {
	// ID names the provider in URLs and the database: lowercase letters, digits and dashes.
	ID string
	// Name is what the sign-in page shows.
	Name string
	// Issuer is the provider's issuer URL, whose /.well-known/openid-configuration says the rest.
	Issuer string
	// ClientID and ClientSecret are the client the operator registered at the provider.
	ClientID     string
	ClientSecret string
}

// Provider is one identity provider. It discovers the provider's endpoints and keys on first use, and again after a
// failure, so a provider that is down at startup fails sign-ins, not the server.
type Provider struct {
	cfg         Config
	redirectURL string
	client      *http.Client

	mu       sync.Mutex
	provider *gooidc.Provider
}

var _ auth.Provider = (*Provider)(nil)

// New returns the provider cfg describes, whose callback is redirectURL, the Auth server's /callback. client makes
// its requests; nil is http.DefaultClient.
func New(cfg Config, redirectURL string, client *http.Client) (*Provider, error) {
	if cfg.ID == "" || cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("provider %q needs an id, an issuer and a clientId", cfg.ID)
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("provider %q has no client secret", cfg.ID)
	}
	if cfg.Name == "" {
		cfg.Name = cfg.ID
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Provider{cfg: cfg, redirectURL: redirectURL, client: client}, nil
}

// ID names the provider.
func (p *Provider) ID() string { return p.cfg.ID }

// Name is what the sign-in page shows.
func (p *Provider) Name() string { return p.cfg.Name }

// discover reads the provider's discovery document once, keeping it; a failure is tried again next time.
func (p *Provider) discover(ctx context.Context) (*gooidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provider != nil {
		return p.provider, nil
	}
	provider, err := gooidc.NewProvider(gooidc.ClientContext(ctx, p.client), p.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discovering %s at %s: %w", p.cfg.ID, p.cfg.Issuer, err)
	}
	p.provider = provider
	return provider, nil
}

func (p *Provider) oauth2Config(provider *gooidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.redirectURL,
		Scopes:       []string{gooidc.ScopeOpenID, "email", "profile"},
	}
}

// AuthCodeURL is where the browser signs in: the provider's authorization endpoint, with state, nonce and the PKCE
// challenge of codeVerifier.
func (p *Provider) AuthCodeURL(ctx context.Context, state, nonce, codeVerifier string) (string, error) {
	provider, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	return p.oauth2Config(provider).AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(codeVerifier)), nil
}

// Exchange trades code for the provider's tokens, verifies the ID token's signature, issuer, audience, expiry and
// nonce, and returns what it says about the person.
func (p *Provider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (auth.Claims, error) {
	provider, err := p.discover(ctx)
	if err != nil {
		return auth.Claims{}, err
	}
	ctx = gooidc.ClientContext(ctx, p.client)
	token, err := p.oauth2Config(provider).Exchange(ctx, code, oauth2.VerifierOption(codeVerifier))
	if err != nil {
		return auth.Claims{}, fmt.Errorf("exchanging the code at %s: %w", p.cfg.ID, err)
	}
	raw, _ := token.Extra("id_token").(string)
	if raw == "" {
		return auth.Claims{}, fmt.Errorf("%s returned no ID token", p.cfg.ID)
	}
	idToken, err := provider.VerifierContext(ctx, &gooidc.Config{ClientID: p.cfg.ClientID}).Verify(ctx, raw)
	if err != nil {
		return auth.Claims{}, fmt.Errorf("verifying %s's ID token: %w", p.cfg.ID, err)
	}
	if idToken.Nonce != nonce {
		return auth.Claims{}, errors.New("the ID token's nonce is not the sign-in's")
	}
	var c struct {
		Email         string  `json:"email"`
		EmailVerified boolish `json:"email_verified"`
		Name          string  `json:"name"`
	}
	if err := idToken.Claims(&c); err != nil {
		return auth.Claims{}, fmt.Errorf("reading %s's claims: %w", p.cfg.ID, err)
	}
	return auth.Claims{Issuer: idToken.Issuer, Subject: idToken.Subject, Email: c.Email, EmailVerified: bool(c.EmailVerified), Name: c.Name}, nil
}

// boolish is a boolean claim some providers send as the string "true".
type boolish bool

func (b *boolish) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("reading a boolean claim: %w", err)
	}
	switch v := v.(type) {
	case bool:
		*b = boolish(v)
	case string:
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("reading a boolean claim from %q: %w", v, err)
		}
		*b = boolish(parsed)
	case nil:
		*b = false
	default:
		return fmt.Errorf("a boolean claim is %T", v)
	}
	return nil
}
