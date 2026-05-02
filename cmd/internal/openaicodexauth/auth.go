package openaicodexauth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ClientID           = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultAuthBaseURL = "https://auth.openai.com"
	providerKey        = "openai-codex"
	refreshMargin      = 5 * time.Minute
)

type Config struct {
	AuthBaseURL string
	AuthPath    string
	HTTPClient  *http.Client
	Now         func() time.Time
	PollDelay   *time.Duration
}

type Token struct {
	Type         string `json:"type"`
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id"`
}

type Manager struct {
	cfg Config
}

func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg.withDefaults()}
}

func DefaultPath() string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
		return filepath.Join(xdg, "clnkr", "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".clnkr", "auth.json")
	}
	return filepath.Join(home, ".local", "share", "clnkr", "auth.json")
}

func DeviceLogin(ctx context.Context, w io.Writer, cfg Config) error {
	cfg = cfg.withDefaults()
	device, err := requestDeviceCode(ctx, cfg)
	if err != nil {
		return err
	}
	lines := []string{
		"",
		"Follow these steps to sign in with ChatGPT using device code authorization:",
		"",
		"1. Open this link in your browser and sign in to your account",
		"   " + device.VerificationURL,
		"",
		"2. Enter this one-time code (expires in 15 minutes)",
		"   " + device.UserCode,
		"",
		"Device codes are a common phishing target. Never share this code.",
		"",
		"Waiting for authorization...",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	code, err := pollDeviceCode(ctx, cfg, device)
	if err != nil {
		return err
	}
	tok, err := exchangeCode(ctx, cfg, code.AuthorizationCode, code.CodeVerifier)
	if err != nil {
		return err
	}
	if tok.AccountID == "" {
		return fmt.Errorf("token response missing chatgpt account id")
	}
	if err := Save(cfg.AuthPath, tok); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Successfully logged in"); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Authorize(ctx context.Context, req *http.Request) error {
	tok, err := m.validToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("ChatGPT-Account-ID", tok.AccountID)
	req.Header.Set("originator", "clnkr")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "clnkr")
	}
	return nil
}

func (m *Manager) HandleUnauthorized(ctx context.Context) (bool, error) {
	if err := m.refresh(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) refresh(ctx context.Context) error {
	tok, err := Load(m.cfg.AuthPath)
	if err != nil {
		return fmt.Errorf("load openai-codex auth: %w", err)
	}
	refreshed, err := refreshToken(ctx, m.cfg, tok)
	if err != nil {
		return err
	}
	return Save(m.cfg.AuthPath, refreshed)
}

func (m *Manager) validToken(ctx context.Context) (Token, error) {
	tok, err := Load(m.cfg.AuthPath)
	if err != nil {
		return Token{}, fmt.Errorf("openai-codex auth is missing; run clnkr --login-openai-codex: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" || tok.AccountID == "" {
		return Token{}, fmt.Errorf("openai-codex auth is incomplete; run clnkr --login-openai-codex")
	}
	if !tok.nearExpiry(m.cfg.now()) {
		return tok, nil
	}
	if err := m.refresh(ctx); err != nil {
		return Token{}, err
	}
	return Load(m.cfg.AuthPath)
}

func Save(path string, tok Token) error {
	if tok.Type == "" {
		tok.Type = "oauth"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth directory: %w", err)
	}
	data := map[string]json.RawMessage{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &data); err != nil {
			return fmt.Errorf("parse auth file: %w", err)
		}
		if data == nil {
			return fmt.Errorf("parse auth file: root must be an object")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read auth file: %w", err)
	}
	tokenBody, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	data[providerKey] = tokenBody
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth file: %w", err)
	}
	body = append(body, '\n')
	tmp, err := os.CreateTemp(dir, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("create auth temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod auth temp file: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write auth temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close auth temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace auth file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod auth file: %w", err)
	}
	return nil
}

func Load(path string) (Token, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Token{}, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		return Token{}, fmt.Errorf("parse auth file: %w", err)
	}
	entry, ok := data[providerKey]
	if !ok {
		return Token{}, fmt.Errorf("auth file does not contain %q", providerKey)
	}
	var tok Token
	if err := json.Unmarshal(entry, &tok); err != nil {
		return Token{}, fmt.Errorf("parse %s auth entry: %w", providerKey, err)
	}
	return tok, nil
}

func AccountIDFromJWT(token string) (string, error) {
	claims, err := jwtClaims(token)
	if err != nil {
		return "", err
	}
	if id, ok := claims["chatgpt_account_id"].(string); ok && id != "" {
		return id, nil
	}
	if nested, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := nested["chatgpt_account_id"].(string); ok && id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("missing chatgpt account id claim")
}

func jwtExpiration(token string) (time.Time, bool) {
	claims, err := jwtClaims(token)
	if err != nil {
		return time.Time{}, false
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(exp), 0), true
}

func jwtClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil, fmt.Errorf("invalid JWT")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(body, &claims); err != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", err)
	}
	return claims, nil
}

type deviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        int
}

type codeResponse struct {
	AuthorizationCode string
	CodeVerifier      string
}

func requestDeviceCode(ctx context.Context, cfg Config) (deviceCode, error) {
	endpoint := strings.TrimRight(cfg.AuthBaseURL, "/") + "/api/accounts/deviceauth/usercode"
	body, err := json.Marshal(map[string]string{"client_id": ClientID})
	if err != nil {
		return deviceCode{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return deviceCode{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cfg.client().Do(req)
	if err != nil {
		return deviceCode{}, fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return deviceCode{}, fmt.Errorf("request device code: status %d: %s", resp.StatusCode, readSmall(resp.Body))
	}
	var wire struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return deviceCode{}, fmt.Errorf("decode device code: %w", err)
	}
	interval := 0
	switch v := wire.Interval.(type) {
	case float64:
		interval = int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			interval = i
		}
	}
	return deviceCode{
		VerificationURL: strings.TrimRight(cfg.AuthBaseURL, "/") + "/codex/device",
		UserCode:        wire.UserCode,
		DeviceAuthID:    wire.DeviceAuthID,
		Interval:        interval,
	}, nil
}

func pollDeviceCode(ctx context.Context, cfg Config, device deviceCode) (codeResponse, error) {
	endpoint := strings.TrimRight(cfg.AuthBaseURL, "/") + "/api/accounts/deviceauth/token"
	interval := time.Duration(device.Interval) * time.Second
	expiresAt := cfg.now().Add(15 * time.Minute)
	for {
		body, err := json.Marshal(map[string]string{"device_auth_id": device.DeviceAuthID, "user_code": device.UserCode})
		if err != nil {
			return codeResponse{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return codeResponse{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := cfg.client().Do(req)
		if err != nil {
			return codeResponse{}, fmt.Errorf("poll device code: %w", err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			var wire struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			err := json.NewDecoder(resp.Body).Decode(&wire)
			_ = resp.Body.Close()
			if err != nil {
				return codeResponse{}, fmt.Errorf("decode device token: %w", err)
			}
			return codeResponse{AuthorizationCode: wire.AuthorizationCode, CodeVerifier: wire.CodeVerifier}, nil
		}
		status := resp.StatusCode
		msg := readSmall(resp.Body)
		_ = resp.Body.Close()
		code := deviceErrorCode(msg)
		switch code {
		case "", "authorization_pending", "deviceauth_authorization_pending", "deviceauth_authorization_unknown":
		case "slow_down":
			interval += time.Second
		case "access_denied", "authorization_denied":
			return codeResponse{}, fmt.Errorf("device auth denied")
		case "expired", "expired_token":
			return codeResponse{}, fmt.Errorf("device auth expired")
		default:
			if status == http.StatusForbidden || status == http.StatusNotFound {
				return codeResponse{}, fmt.Errorf("device auth failed: %s", code)
			}
		}
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return codeResponse{}, fmt.Errorf("device auth failed: status %d: %s", status, msg)
		}
		if !cfg.now().Before(expiresAt) {
			return codeResponse{}, fmt.Errorf("device auth timed out after 15 minutes")
		}
		if interval <= 0 {
			interval = time.Second
		}
		delay := interval
		if cfg.PollDelay != nil {
			delay = *cfg.PollDelay
		}
		select {
		case <-ctx.Done():
			return codeResponse{}, ctx.Err()
		case <-time.After(delay):
		}
	}
}

func deviceErrorCode(body string) string {
	var wire struct {
		Error any `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &wire); err != nil {
		return ""
	}
	switch value := wire.Error.(type) {
	case string:
		return value
	case map[string]any:
		if code, ok := value["code"].(string); ok {
			return code
		}
	}
	return ""
}

func exchangeCode(ctx context.Context, cfg Config, code, verifier string) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {strings.TrimRight(cfg.AuthBaseURL, "/") + "/deviceauth/callback"},
		"client_id":     {ClientID},
		"code_verifier": {verifier},
	}
	return postToken(ctx, cfg, form)
}

func refreshToken(ctx context.Context, cfg Config, current Token) (Token, error) {
	form := url.Values{
		"client_id":     {ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {current.RefreshToken},
	}
	tok, err := postToken(ctx, cfg, form)
	if err != nil {
		return Token{}, err
	}
	if tok.IDToken == "" {
		tok.IDToken = current.IDToken
	}
	if tok.AccountID == "" {
		tok.AccountID = current.AccountID
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = current.RefreshToken
	}
	return tok, nil
}

func postToken(ctx context.Context, cfg Config, form url.Values) (Token, error) {
	endpoint := strings.TrimRight(cfg.AuthBaseURL, "/") + "/oauth/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cfg.client().Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Token{}, fmt.Errorf("token request: status %d: %s", resp.StatusCode, readSmall(resp.Body))
	}
	var wire struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil {
		return Token{}, fmt.Errorf("decode token response: %w", err)
	}
	var accountID string
	for _, token := range []string{wire.IDToken, wire.AccessToken} {
		if token == "" {
			continue
		}
		if id, err := AccountIDFromJWT(token); err == nil {
			accountID = id
			break
		}
	}
	expiresAt := cfg.now().Add(time.Hour).UnixMilli()
	if wire.ExpiresIn > 0 {
		expiresAt = cfg.now().Add(time.Duration(wire.ExpiresIn) * time.Second).UnixMilli()
	} else if exp, ok := jwtExpiration(wire.AccessToken); ok {
		expiresAt = exp.UnixMilli()
	}
	return Token{
		Type:         "oauth",
		IDToken:      wire.IDToken,
		AccessToken:  wire.AccessToken,
		RefreshToken: wire.RefreshToken,
		ExpiresAt:    expiresAt,
		AccountID:    accountID,
	}, nil
}

func (t Token) nearExpiry(now time.Time) bool {
	return t.ExpiresAt == 0 || now.Add(refreshMargin).UnixMilli() >= t.ExpiresAt
}

func (c Config) withDefaults() Config {
	if c.AuthBaseURL == "" {
		c.AuthBaseURL = DefaultAuthBaseURL
	}
	if c.AuthPath == "" {
		c.AuthPath = DefaultPath()
	}
	return c
}

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func readSmall(r io.Reader) string {
	body, _ := io.ReadAll(io.LimitReader(r, 4096))
	return strings.TrimSpace(string(body))
}
