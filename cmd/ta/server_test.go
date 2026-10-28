package main

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

const (
	testToken = "test-token"
	testHost  = "127.0.0.1:7340"
)

func newTestVault(t *testing.T) *vault {
	t.Helper()
	key := make([]byte, keySize)
	rand.Read(key)
	v, err := newVault(vaultLocation{dir: t.TempDir()}, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.save(environments{}); err != nil {
		t.Fatal(err)
	}
	return v
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	return newServer(newTestVault(t), testToken, []string{testHost})
}

func serve(h http.Handler, method, path, body, host string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+testHost+path, strings.NewReader(body))
	req.Host = host
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// listSecrets returns the API listing as ENV/NAME strings.
func listSecrets(t *testing.T, h http.Handler) []string {
	t.Helper()
	rec := serve(h, "GET", "/api/secrets", "", testHost, map[string]string{"Authorization": "Bearer " + testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var infos []secretInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &infos); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, i := range infos {
		out = append(out, i.Env+"/"+i.Name)
	}
	return out
}

func TestServerRejectsRequestsNotFromItsOwnPage(t *testing.T) {
	h := newTestServer(t)
	auth := "Bearer " + testToken
	cases := []struct {
		name   string
		host   string
		header map[string]string
		want   int
	}{
		{"no token", testHost, nil, http.StatusUnauthorized},
		{"wrong token", testHost, map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"token without Bearer scheme", testHost, map[string]string{"Authorization": testToken}, http.StatusUnauthorized},
		{"DNS rebinding Host", "evil.example:7340", map[string]string{"Authorization": auth}, http.StatusForbidden},
		{"other site Origin", testHost, map[string]string{"Authorization": auth, "Origin": "http://evil.example"}, http.StatusForbidden},
		{"https Origin", testHost, map[string]string{"Authorization": auth, "Origin": "https://" + testHost}, http.StatusForbidden},
		{"own page", testHost, map[string]string{"Authorization": auth, "Origin": "http://" + testHost}, http.StatusNoContent},
	}
	for _, tc := range cases {
		rec := serve(h, "PUT", "/api/secrets/dev/X", `{"value":"v"}`, tc.host, tc.header)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
	if got := listSecrets(t, h); !slices.Equal(got, []string{"dev/X"}) {
		t.Fatalf("vault after requests = %v, want only the own-page write [dev/X]", got)
	}
}

func TestServerAPINeverReturnsValues(t *testing.T) {
	h := newTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + testToken}

	if rec := serve(h, "PUT", "/api/secrets/dev/API_KEY", `{"value":"sk-secret-value"}`, testHost, auth); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	rec := serve(h, "GET", "/api/secrets", "", testHost, auth)
	if !strings.Contains(rec.Body.String(), "API_KEY") || strings.Contains(rec.Body.String(), "sk-secret-value") {
		t.Fatalf("list must show the name and hide the value: %s", rec.Body)
	}

	for _, tc := range []struct{ path, body string }{
		{"/api/secrets/dev/9BAD", `{"value":"v"}`},
		{"/api/secrets/-dev/OK", `{"value":"v"}`},
		{"/api/secrets/dev/EMPTY", `{"value":""}`},
		{"/api/secrets/dev/RAW", `not json`},
	} {
		if rec := serve(h, "PUT", tc.path, tc.body, testHost, auth); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %s %s: status %d, want 400", tc.path, tc.body, rec.Code)
		}
	}
}

func TestServerKeepsEnvironmentsSeparate(t *testing.T) {
	h := newTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + testToken}
	for _, path := range []string{"/api/secrets/dev/DB_URL", "/api/secrets/prod/DB_URL"} {
		if rec := serve(h, "PUT", path, `{"value":"v"}`, testHost, auth); rec.Code != http.StatusNoContent {
			t.Fatalf("put %s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if rec := serve(h, "DELETE", "/api/secrets/prod/DB_URL", "", testHost, auth); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if got := listSecrets(t, h); !slices.Equal(got, []string{"dev/DB_URL"}) {
		t.Fatalf("after deleting prod/DB_URL: %v, want [dev/DB_URL]", got)
	}
	if rec := serve(h, "DELETE", "/api/secrets/prod/DB_URL", "", testHost, auth); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d, want 404", rec.Code)
	}
}
