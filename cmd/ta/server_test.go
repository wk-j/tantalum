package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestServerCopiesRenamesAndDeletesEnvironmentsAndNames(t *testing.T) {
	h := newTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + testToken}
	call := func(method, path, body string, want int) string {
		t.Helper()
		rec := serve(h, method, path, body, testHost, auth)
		if rec.Code != want {
			t.Fatalf("%s %s %s: status %d, want %d (%s)", method, path, body, rec.Code, want, rec.Body)
		}
		return strings.TrimSpace(rec.Body.String())
	}
	expect := func(step string, want ...string) {
		t.Helper()
		if got := listSecrets(t, h); !slices.Equal(got, want) {
			t.Fatalf("after %s: %v, want %v", step, got, want)
		}
	}
	for _, p := range []string{"dev/A", "dev/B", "prod/A"} {
		call("PUT", "/api/secrets/"+p, `{"value":"v-`+p+`"}`, http.StatusNoContent)
	}

	if got := call("POST", "/api/envs/dev/copy", `{"to":"prod"}`, http.StatusOK); got != `{"copied":["B"],"skipped":["A"]}` {
		t.Fatalf("copy without overwrite answered %s", got)
	}
	if got := call("POST", "/api/envs/dev/copy", `{"to":"prod","overwrite":true}`, http.StatusOK); got != `{"copied":["A","B"],"skipped":[]}` {
		t.Fatalf("copy with overwrite answered %s", got)
	}
	call("POST", "/api/envs/dev/copy", `{"to":"dev"}`, http.StatusBadRequest)
	call("POST", "/api/envs/nope/copy", `{"to":"x"}`, http.StatusNotFound)

	call("POST", "/api/envs/prod/rename", `{"to":"dev"}`, http.StatusConflict)
	call("POST", "/api/envs/prod/rename", `{"to":"staging"}`, http.StatusNoContent)
	expect("renaming prod to staging", "dev/A", "dev/B", "staging/A", "staging/B")

	// A taken name in any environment leaves every environment unchanged.
	call("POST", "/api/names/A/rename", `{"to":"B"}`, http.StatusConflict)
	expect("a refused rename", "dev/A", "dev/B", "staging/A", "staging/B")
	call("POST", "/api/names/A/rename", `{"to":"9X"}`, http.StatusBadRequest)
	call("POST", "/api/names/A/rename", `{"to":"C"}`, http.StatusNoContent)
	expect("renaming A to C", "dev/B", "dev/C", "staging/B", "staging/C")

	call("DELETE", "/api/names/C", "", http.StatusNoContent)
	call("DELETE", "/api/names/C", "", http.StatusNotFound)
	expect("deleting C everywhere", "dev/B", "staging/B")

	call("DELETE", "/api/envs/staging", "", http.StatusNoContent)
	call("DELETE", "/api/envs/staging", "", http.StatusNotFound)
	expect("deleting staging", "dev/B")
}

// stubReveal answers the Mac's prompt with answer and copies to a private
// pasteboard; instead of starting a clearer process it records the change
// count each copy hands to the clearer.
func stubReveal(t *testing.T, answer func(reason string) error) *[]int {
	t.Helper()
	confirm, clearer, board := confirmUser, clearClipboardLater, pasteboardName
	t.Cleanup(func() { confirmUser, clearClipboardLater, pasteboardName = confirm, clearer, board })
	confirmUser = answer
	pasteboardName = fmt.Sprintf("dev.tantalum.test.%d", os.Getpid())
	counts := &[]int{}
	clearClipboardLater = func(count int) error {
		*counts = append(*counts, count)
		return nil
	}
	return counts
}

// The web UI's reveal asks the Mac with a prompt that names the secret and the
// clipboard, and copies the value only once the person approves.
func TestServerRevealCopiesOnlyAfterTheMacApproves(t *testing.T) {
	var reasons []string
	refuse := true
	clears := stubReveal(t, func(reason string) error {
		reasons = append(reasons, reason)
		if refuse {
			return errors.New("Canceled by user.")
		}
		return nil
	})
	h := newTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + testToken}
	if rec := serve(h, "PUT", "/api/secrets/dev/API_KEY", `{"value":"sk-reveal-value"}`, testHost, auth); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	reveal := func(path string, want int) string {
		t.Helper()
		rec := serve(h, "POST", path, "", testHost, auth)
		if rec.Code != want {
			t.Fatalf("POST %s: status %d, want %d (%s)", path, rec.Code, want, rec.Body)
		}
		return strings.TrimSpace(rec.Body.String())
	}

	reveal("/api/secrets/prod/API_KEY/reveal", http.StatusNotFound)
	if len(reasons) != 0 {
		t.Fatalf("a missing secret raised the prompt %q", reasons)
	}

	reveal("/api/secrets/dev/API_KEY/reveal", http.StatusForbidden)
	if len(*clears) != 0 {
		t.Fatal("a refused prompt still copied the value")
	}

	refuse = false
	if got := reveal("/api/secrets/dev/API_KEY/reveal", http.StatusOK); got != `{"clearSeconds":45}` {
		t.Fatalf("approved reveal answered %s", got)
	}
	if len(*clears) != 1 || !clearClipboardIfUnchanged((*clears)[0]) {
		t.Fatalf("approved reveal handed %v to the clearer; want the change count of its copy, the latest on the pasteboard", *clears)
	}
	for _, reason := range reasons {
		if !strings.Contains(reason, "dev/API_KEY") || !strings.Contains(reason, "clipboard") {
			t.Errorf("prompt %q does not name the secret and the clipboard", reason)
		}
	}
}

// A second ta serve, for another project, must not fail on the default port,
// but an address the human chose with -addr must not silently change.
func TestListenServeFallsBackOnlyFromTheDefaultAddress(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	addr := taken.Addr().String()

	if ln, err := listenServe(addr, false); err == nil {
		ln.Close()
		t.Errorf("-addr %s is in use: listened anyway; want an error", addr)
	}
	ln, err := listenServe(addr, true)
	if err != nil {
		t.Fatalf("default address %s is in use: %v; want a free port", addr, err)
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr); !got.IP.IsLoopback() || got.String() == addr {
		t.Errorf("fallback listens on %s; want another loopback port", got)
	}
}
