package openaicodexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoadTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	tok := Token{
		Type:         "oauth",
		IDToken:      jwtWithClaims(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-123"}}),
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Unix(2000, 0).UnixMilli(),
		AccountID:    "acct-123",
	}

	if err := Save(path, tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat auth file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "access" || loaded.RefreshToken != "refresh" || loaded.AccountID != "acct-123" {
		t.Fatalf("loaded token = %+v", loaded)
	}
}

func TestSavePreservesOtherAuthEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	body := []byte(`{"other-provider":{"type":"oauth","access_token":"other-access","extra":{"kept":true}}}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Save(path, Token{AccessToken: "access", RefreshToken: "refresh", AccountID: "acct"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	var other struct {
		Type        string `json:"type"`
		AccessToken string `json:"access_token"`
		Extra       struct {
			Kept bool `json:"kept"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(got["other-provider"], &other); err != nil {
		t.Fatal(err)
	}
	if other.AccessToken != "other-access" || !other.Extra.Kept {
		t.Fatalf("other provider = %+v, want unknown fields preserved", other)
	}
	var codex Token
	if err := json.Unmarshal(got["openai-codex"], &codex); err != nil {
		t.Fatal(err)
	}
	if codex.AccessToken != "access" {
		t.Fatalf("openai-codex = %+v, want updated", codex)
	}
}

func TestSaveRepairsExistingAuthFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"openai-codex":{"access_token":"old"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Token{AccessToken: "access", RefreshToken: "refresh", AccountID: "acct"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %#o, want 0600", got)
	}
}

func TestSaveRejectsNullAuthFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`null`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Token{AccessToken: "access"}); err == nil {
		t.Fatal("Save succeeded, want invalid auth file error")
	}
}

func TestAccountIDFromJWTClaims(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name: "nested auth claim",
			claims: map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-nested"},
			},
			want: "acct-nested",
		},
		{
			name:   "top level claim",
			claims: map[string]any{"chatgpt_account_id": "acct-top"},
			want:   "acct-top",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AccountIDFromJWT(jwtWithClaims(t, tt.claims))
			if err != nil {
				t.Fatalf("AccountIDFromJWT: %v", err)
			}
			if got != tt.want {
				t.Fatalf("account id = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeviceLoginSavesTokensAndPrintsPrompt(t *testing.T) {
	var seenUserCodeRequest bool
	var seenPollRequest bool
	var seenTokenExchange bool

	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			seenUserCodeRequest = true
			body := readJSONBody(t, r)
			assertJSONField(t, body, "client_id", ClientID)
			writeJSON(t, w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			seenPollRequest = true
			body := readJSONBody(t, r)
			assertJSONField(t, body, "device_auth_id", "device-1")
			assertJSONField(t, body, "user_code", "ABCD-EFGH")
			writeJSON(t, w, map[string]any{
				"authorization_code": "auth-code",
				"code_verifier":      "verifier",
				"code_challenge":     "challenge",
			})
		case "/oauth/token":
			seenTokenExchange = true
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got, want := r.FormValue("grant_type"), "authorization_code"; got != want {
				t.Fatalf("grant_type = %q, want %q", got, want)
			}
			if got, want := r.FormValue("code"), "auth-code"; got != want {
				t.Fatalf("code = %q, want %q", got, want)
			}
			if got, want := r.FormValue("redirect_uri"), authServerURL(r)+"/deviceauth/callback"; got != want {
				t.Fatalf("redirect_uri = %q, want %q", got, want)
			}
			writeJSON(t, w, map[string]any{
				"id_token": jwtWithClaims(t, map[string]any{"sub": "user-123"}),
				"access_token": jwtWithClaims(t, map[string]any{
					"exp": time.Now().Add(time.Hour).Unix(),
					"https://api.openai.com/auth": map[string]any{
						"chatgpt_account_id": "acct-123",
					},
				}),
				"refresh_token": "refresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	path := filepath.Join(t.TempDir(), "auth.json")
	var out strings.Builder
	err := DeviceLogin(context.Background(), &out, Config{
		AuthBaseURL: authServer.URL,
		AuthPath:    path,
		HTTPClient:  authServer.Client(),
		Now:         func() time.Time { return time.Unix(1000, 0) },
	})
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if !seenUserCodeRequest || !seenPollRequest || !seenTokenExchange {
		t.Fatalf("requests seen user=%v poll=%v exchange=%v", seenUserCodeRequest, seenPollRequest, seenTokenExchange)
	}
	if !strings.Contains(out.String(), "/codex/device") || !strings.Contains(out.String(), "ABCD-EFGH") {
		t.Fatalf("prompt = %q, want URL and code", out.String())
	}
	tok, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tok.AccountID != "acct-123" || tok.RefreshToken != "refresh-token" {
		t.Fatalf("token = %+v", tok)
	}
}

func TestDeviceLoginHandlesDeniedDeviceCode(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       0,
			})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusForbidden)
			writeJSON(t, w, map[string]any{"error": "access_denied"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	zero := time.Duration(0)
	err := DeviceLogin(context.Background(), io.Discard, Config{
		AuthBaseURL: authServer.URL,
		AuthPath:    filepath.Join(t.TempDir(), "auth.json"),
		HTTPClient:  authServer.Client(),
		PollDelay:   &zero,
	})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("DeviceLogin error = %v, want denied", err)
	}
}

func TestDeviceLoginHandlesExpiredDeviceCode(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       0,
			})
		case "/api/accounts/deviceauth/token":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"error": map[string]any{"code": "expired_token"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	zero := time.Duration(0)
	err := DeviceLogin(context.Background(), io.Discard, Config{
		AuthBaseURL: authServer.URL,
		AuthPath:    filepath.Join(t.TempDir(), "auth.json"),
		HTTPClient:  authServer.Client(),
		PollDelay:   &zero,
	})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("DeviceLogin error = %v, want expired", err)
	}
}

func TestDeviceLoginRetriesSlowDown(t *testing.T) {
	var polls int
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       0,
			})
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				writeJSON(t, w, map[string]any{"error": "slow_down"})
				return
			}
			writeJSON(t, w, map[string]any{
				"authorization_code": "auth-code",
				"code_verifier":      "verifier",
				"code_challenge":     "challenge",
			})
		case "/oauth/token":
			writeJSON(t, w, map[string]any{
				"id_token":      jwtWithClaims(t, map[string]any{"chatgpt_account_id": "acct-123"}),
				"access_token":  jwtWithClaims(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
				"refresh_token": "refresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	zero := time.Duration(0)
	err := DeviceLogin(context.Background(), io.Discard, Config{
		AuthBaseURL: authServer.URL,
		AuthPath:    filepath.Join(t.TempDir(), "auth.json"),
		HTTPClient:  authServer.Client(),
		PollDelay:   &zero,
	})
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
}

func TestDeviceLoginRetriesAuthorizationUnknown(t *testing.T) {
	var polls int
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]any{
				"device_auth_id": "device-1",
				"user_code":      "ABCD-EFGH",
				"interval":       "0",
			})
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusForbidden)
				writeJSON(t, w, map[string]any{"error": map[string]any{"code": "deviceauth_authorization_unknown"}})
				return
			}
			writeJSON(t, w, map[string]any{
				"authorization_code": "auth-code",
				"code_verifier":      "verifier",
				"code_challenge":     "challenge",
			})
		case "/oauth/token":
			writeJSON(t, w, map[string]any{
				"id_token":      jwtWithClaims(t, map[string]any{"chatgpt_account_id": "acct-123"}),
				"access_token":  jwtWithClaims(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
				"refresh_token": "refresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authServer.Close()

	zero := time.Duration(0)
	err := DeviceLogin(context.Background(), io.Discard, Config{
		AuthBaseURL: authServer.URL,
		AuthPath:    filepath.Join(t.TempDir(), "auth.json"),
		HTTPClient:  authServer.Client(),
		PollDelay:   &zero,
	})
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
}

func TestManagerRefreshPreservesAccountWhenIDTokenIsOmitted(t *testing.T) {
	var seenRefresh bool
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		seenRefresh = true
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got, want := r.FormValue("grant_type"), "refresh_token"; got != want {
			t.Fatalf("grant_type = %q, want %q", got, want)
		}
		writeJSON(t, w, map[string]any{
			"access_token":  jwtWithClaims(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
			"refresh_token": "new-refresh",
		})
	}))
	defer authServer.Close()

	path := filepath.Join(t.TempDir(), "auth.json")
	idToken := jwtWithClaims(t, map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-123"}})
	if err := Save(path, Token{
		Type:         "oauth",
		IDToken:      idToken,
		AccessToken:  "expired",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
		AccountID:    "acct-123",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	mgr := NewManager(Config{AuthBaseURL: authServer.URL, AuthPath: path, HTTPClient: authServer.Client()})
	if retry, err := mgr.HandleUnauthorized(context.Background()); err != nil || !retry {
		t.Fatalf("HandleUnauthorized: retry=%v err=%v", retry, err)
	}
	if !seenRefresh {
		t.Fatal("refresh endpoint was not called")
	}
	tok, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tok.IDToken != idToken || tok.AccountID != "acct-123" || tok.RefreshToken != "new-refresh" {
		t.Fatalf("token after refresh = %+v", tok)
	}
}

func jwtWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".sig"
}

func readJSONBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return body
}

func assertJSONField(t *testing.T, body map[string]any, name, want string) {
	t.Helper()
	if got := body[name]; got != want {
		t.Fatalf("%s = %#v, want %q", name, got, want)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func authServerURL(r *http.Request) string {
	return fmt.Sprintf("http://%s", r.Host)
}
