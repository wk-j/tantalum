package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

const (
	defaultServeAddr = "127.0.0.1:7340"
	maxRequestBody   = 1 << 20
)

func cmdServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addr := flags.String("addr", defaultServeAddr, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("usage: ta serve [-addr HOST:PORT]")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("refusing to listen on %s: the web UI binds to loopback addresses only", host)
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hosts := []string{ln.Addr().String(), fmt.Sprintf("localhost:%d", ln.Addr().(*net.TCPAddr).Port)}
	srv := &http.Server{
		Handler:           newServer(v, token, hosts),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	// The token travels in the URL fragment, which browsers never send to the
	// server or store in Referer headers; the page moves it into sessionStorage.
	fmt.Fprintf(os.Stderr, "ta: vault %s\nta: web UI on http://%s/#token=%s\nta: anyone holding this URL can change secrets (never read them); press Ctrl-C to stop\n",
		v.vaultLocation, ln.Addr(), token)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// server is the web UI backend. Its API is write-only for values: it lists
// environments, names and update times, stores, copies, renames and deletes
// secrets (copies and renames move values inside the vault only), and never
// returns a value.
type server struct {
	vault *vault
	token []byte
	hosts map[string]bool // Host header values this server answers to
}

func newServer(v *vault, token string, hosts []string) http.Handler {
	s := &server{vault: v, token: []byte(token), hosts: map[string]bool{}}
	for _, h := range hosts {
		s.hosts[h] = true
	}
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // embedded at build time
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /api/vault", s.info)
	mux.HandleFunc("GET /api/secrets", s.list)
	mux.HandleFunc("PUT /api/secrets/{env}/{name}", s.put)
	mux.HandleFunc("DELETE /api/secrets/{env}/{name}", s.delete)
	mux.HandleFunc("POST /api/envs/{env}/copy", s.copyEnv)
	mux.HandleFunc("POST /api/envs/{env}/rename", s.renameEnv)
	mux.HandleFunc("DELETE /api/envs/{env}", s.deleteEnv)
	mux.HandleFunc("POST /api/names/{name}/rename", s.renameName)
	mux.HandleFunc("DELETE /api/names/{name}", s.deleteName)
	return s.guard(mux)
}

// guard rejects requests that did not come from this server's own page:
//   - Host must be this server, which defeats DNS rebinding (a hostile site
//     whose domain resolves to 127.0.0.1 still sends its own Host);
//   - Origin, when present, must be this server, which blocks other sites;
//   - /api/ requires the bearer token. Browsers only attach an Authorization
//     header cross-origin after a CORS preflight, which this server never
//     approves, so other pages cannot forge API calls.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[r.Host] {
			writeError(w, http.StatusForbidden, "unexpected Host header")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			host, ok := strings.CutPrefix(origin, "http://")
			if !ok || !s.hosts[host] {
				writeError(w, http.StatusForbidden, "cross-origin request")
				return
			}
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || subtle.ConstantTimeCompare([]byte(token), s.token) != 1 {
				writeError(w, http.StatusUnauthorized, "missing or wrong token; open the URL printed by `ta serve`")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// info tells the page which vault it manages, so several `ta serve` tabs
// (one per project) can be told apart.
func (s *server) info(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"root": s.vault.root, "dir": s.vault.dir})
}

type secretInfo struct {
	Env     string    `json:"env"`
	Name    string    `json:"name"`
	Updated time.Time `json:"updated"`
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	envs, err := s.vault.load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []secretInfo{}
	for _, env := range slices.Sorted(maps.Keys(envs)) {
		for _, name := range slices.Sorted(maps.Keys(envs[env])) {
			out = append(out, secretInfo{Env: env, Name: name, Updated: envs[env][name].Updated})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *server) put(w http.ResponseWriter, r *http.Request) {
	env, name := r.PathValue("env"), r.PathValue("name")
	if !validSlug(env) {
		writeError(w, http.StatusBadRequest, "invalid environment: use letters, digits, _ and -, starting with a letter or digit")
		return
	}
	if !validName(name) {
		writeError(w, http.StatusBadRequest, "invalid name: use letters, digits and _, not starting with a digit")
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON {\"value\": \"...\"} up to 1 MiB")
		return
	}
	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "empty value; nothing stored")
		return
	}
	if err := s.vault.set(env, name, body.Value); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) delete(w http.ResponseWriter, r *http.Request) {
	writeResult(w, s.vault.remove(r.PathValue("env"), r.PathValue("name")))
}

// target is the body of copy and rename requests.
type target struct {
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

func readTarget(w http.ResponseWriter, r *http.Request) (target, bool) {
	var t target
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, `body must be JSON {"to": "..."}`)
		return t, false
	}
	return t, true
}

// envTarget reads the destination environment of a copy or rename.
func envTarget(w http.ResponseWriter, r *http.Request) (target, bool) {
	t, ok := readTarget(w, r)
	if ok && (!validSlug(t.To) || t.To == r.PathValue("env")) {
		writeError(w, http.StatusBadRequest, "invalid target environment: use letters, digits, _ and -, starting with a letter or digit, and not the source")
		return t, false
	}
	return t, ok
}

func (s *server) copyEnv(w http.ResponseWriter, r *http.Request) {
	t, ok := envTarget(w, r)
	if !ok {
		return
	}
	copied, skipped, err := s.vault.copyEnv(r.PathValue("env"), t.To, t.Overwrite)
	if err != nil {
		writeResult(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"copied": copied, "skipped": skipped})
}

func (s *server) renameEnv(w http.ResponseWriter, r *http.Request) {
	if t, ok := envTarget(w, r); ok {
		writeResult(w, s.vault.renameEnv(r.PathValue("env"), t.To))
	}
}

func (s *server) deleteEnv(w http.ResponseWriter, r *http.Request) {
	writeResult(w, s.vault.removeEnv(r.PathValue("env")))
}

func (s *server) renameName(w http.ResponseWriter, r *http.Request) {
	t, ok := readTarget(w, r)
	if !ok {
		return
	}
	if !validName(t.To) || t.To == r.PathValue("name") {
		writeError(w, http.StatusBadRequest, "invalid new name: use letters, digits and _, not starting with a digit, and not the current name")
		return
	}
	writeResult(w, s.vault.renameSecret(r.PathValue("name"), t.To))
}

func (s *server) deleteName(w http.ResponseWriter, r *http.Request) {
	writeResult(w, s.vault.removeSecretEverywhere(r.PathValue("name")))
}

// writeResult answers a change: 204, or the status that fits the vault error.
func writeResult(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errSecretNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, errExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
