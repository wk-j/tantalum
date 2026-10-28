package main

// Architecture tests behind the rails in docs/adr/*.md. Each TestRails* test
// backs a `check:` in .krypton/rails.toml, which runs it by name with
// `go test -run`; a renamed test makes its check match nothing and pass, so
// rename the check's argv with it.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// railsPackage is this package's non-test source, type-checked.
type railsPackage struct {
	fset  *token.FileSet
	files []*ast.File
	pkg   *types.Package
	info  *types.Info
}

var loadRailsPackage = sync.OnceValues(func() (*railsPackage, error) {
	bp, err := build.ImportDir(".", 0)
	if err != nil {
		return nil, err
	}
	p := &railsPackage{
		fset: token.NewFileSet(),
		info: &types.Info{
			Types:      map[ast.Expr]types.TypeAndValue{},
			Defs:       map[*ast.Ident]types.Object{},
			Uses:       map[*ast.Ident]types.Object{},
			Selections: map[*ast.SelectorExpr]*types.Selection{},
		},
	}
	for _, name := range bp.GoFiles {
		f, err := parser.ParseFile(p.fset, name, nil, 0)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(p.fset, "gc", nil)}
	p.pkg, err = conf.Check("main", p.fset, p.files, p.info)
	return p, err
})

func railsLoad(t *testing.T) *railsPackage {
	t.Helper()
	p, err := loadRailsPackage()
	if err != nil {
		t.Fatalf("type-check package sources: %v", err)
	}
	return p
}

func (p *railsPackage) pos(n ast.Node) string {
	pos := p.fset.Position(n.Pos())
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}

func (p *railsPackage) lookup(t *testing.T, name string) types.Object {
	t.Helper()
	obj := p.pkg.Scope().Lookup(name)
	if obj == nil {
		t.Fatalf("package has no %s", name)
	}
	return obj
}

// field returns the field name of the package's struct type typeName.
func (p *railsPackage) field(t *testing.T, typeName, name string) types.Object {
	t.Helper()
	s, ok := p.lookup(t, typeName).Type().Underlying().(*types.Struct)
	if ok {
		for f := range s.Fields() {
			if f.Name() == name {
				return f
			}
		}
	}
	t.Fatalf("type %s has no field %s", typeName, name)
	return nil
}

// railsFuncName names a declaration "name", or "recv.name" for a method.
func railsFuncName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	typ := fd.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func (p *railsPackage) funcDecl(t *testing.T, name string) *ast.FuncDecl {
	t.Helper()
	for _, f := range p.files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && railsFuncName(fd) == name {
				return fd
			}
		}
	}
	t.Fatalf("package has no func %s", name)
	return nil
}

// inspect calls f for every node of the package with the enclosing top-level
// function declaration, nil at package level.
func (p *railsPackage) inspect(f func(n ast.Node, fd *ast.FuncDecl)) {
	for _, file := range p.files {
		ast.PreorderStack(file, nil, func(n ast.Node, stack []ast.Node) bool {
			var fd *ast.FuncDecl
			if len(stack) > 1 {
				fd, _ = stack[1].(*ast.FuncDecl)
			}
			f(n, fd)
			return true
		})
	}
}

func railsName(fd *ast.FuncDecl) string {
	if fd == nil {
		return "package scope"
	}
	return railsFuncName(fd)
}

// callee returns the function or method a call invokes; nil for builtins,
// conversions and calls of function values.
func (p *railsPackage) callee(call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	}
	if id == nil {
		return nil
	}
	f, _ := p.info.Uses[id].(*types.Func)
	if f != nil {
		f = f.Origin()
	}
	return f
}

// railsIs reports whether f is the function pkgPath.name, or the method
// recv.name of pkgPath when recv is not empty.
func railsIs(f *types.Func, pkgPath, recv, name string) bool {
	if f == nil || f.Pkg() == nil || f.Pkg().Path() != pkgPath || f.Name() != name {
		return false
	}
	r := f.Signature().Recv()
	if recv == "" || r == nil {
		return recv == "" && r == nil
	}
	t := r.Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	return ok && named.Obj().Name() == recv
}

// railsDefs maps each variable assigned in a function to the expressions
// assigned to it: a value from a multi-value call is recorded as the call, a
// declaration without a value or a range variable as nil.
type railsDefs map[types.Object][]ast.Expr

func (p *railsPackage) defs(fd *ast.FuncDecl) railsDefs {
	d := railsDefs{}
	record := func(lhs []*ast.Ident, rhs []ast.Expr) {
		for i, id := range lhs {
			obj := p.info.Defs[id]
			if obj == nil {
				obj = p.info.Uses[id]
			}
			if obj == nil {
				continue
			}
			var e ast.Expr
			switch {
			case len(rhs) == len(lhs):
				e = rhs[i]
			case len(rhs) == 1:
				e = rhs[0]
			}
			d[obj] = append(d[obj], e)
		}
	}
	ast.Inspect(fd, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			var lhs []*ast.Ident
			for _, l := range n.Lhs {
				if id, ok := ast.Unparen(l).(*ast.Ident); ok {
					lhs = append(lhs, id)
				} else {
					lhs = append(lhs, ast.NewIdent("_"))
				}
			}
			record(lhs, n.Rhs)
		case *ast.ValueSpec:
			record(n.Names, n.Values)
		case *ast.RangeStmt: // elements of n.X, not n.X itself: no traceable source
			for _, e := range []ast.Expr{n.Key, n.Value} {
				if id, ok := e.(*ast.Ident); ok {
					record([]*ast.Ident{id}, nil)
				}
			}
		}
		return true
	})
	return d
}

// sources returns every expression e can take its value from: variables are
// followed to their assignments in d and conversions are unwrapped. A nil entry
// is a variable with no traceable value.
func (p *railsPackage) sources(d railsDefs, e ast.Expr, seen map[types.Object]bool) []ast.Expr {
	e = ast.Unparen(e)
	switch x := e.(type) {
	case *ast.CallExpr:
		if tv, ok := p.info.Types[x.Fun]; ok && tv.IsType() && len(x.Args) == 1 {
			return p.sources(d, x.Args[0], seen)
		}
	case *ast.Ident:
		obj := p.info.Uses[x]
		if defs, ok := d[obj]; ok {
			if seen[obj] {
				return nil
			}
			seen[obj] = true
			var out []ast.Expr
			for _, def := range defs {
				if def == nil {
					out = append(out, nil)
					continue
				}
				out = append(out, p.sources(d, def, seen)...)
			}
			return out
		}
	}
	return []ast.Expr{e}
}

func (p *railsPackage) describe(n ast.Node) string {
	if n == nil {
		return "a variable with no traceable value"
	}
	var b bytes.Buffer
	if err := printer.Fprint(&b, p.fset, n); err != nil {
		return fmt.Sprintf("%T", n)
	}
	return b.String()
}

// railsHolds reports whether a value of type t can contain a value of type target.
func railsHolds(t, target types.Type, seen map[types.Type]bool) bool {
	if t == nil {
		return false
	}
	if types.Identical(t, target) {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch u := types.Unalias(t).(type) {
	case *types.Named:
		return railsHolds(u.Underlying(), target, seen)
	case *types.Pointer:
		return railsHolds(u.Elem(), target, seen)
	case *types.Slice:
		return railsHolds(u.Elem(), target, seen)
	case *types.Array:
		return railsHolds(u.Elem(), target, seen)
	case *types.Chan:
		return railsHolds(u.Elem(), target, seen)
	case *types.Map:
		return railsHolds(u.Key(), target, seen) || railsHolds(u.Elem(), target, seen)
	case *types.Struct:
		for f := range u.Fields() {
			if railsHolds(f.Type(), target, seen) {
				return true
			}
		}
	}
	return false
}

// railsOutputPackages print, log or serialize their arguments.
var railsOutputPackages = map[string]bool{
	"fmt": true, "log": true, "log/slog": true, "encoding/json": true, "encoding/gob": true,
	"encoding/xml": true, "text/template": true, "html/template": true,
}

// docs/adr/0001: a value is read only to build the child's environment and the
// redactor, and only the vault codec serializes value-holding types.
func TestRailsSecretValuesReachOnlyChildEnvAndRedactor(t *testing.T) {
	p := railsLoad(t)
	secretType := p.lookup(t, "secret").Type()
	valueField := p.field(t, "secret", "Value")
	readers := []string{"cmdRun", "newRedactor"}
	codecs := []string{"vault.load", "vault.save"}
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		fn := railsName(fd)
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if sel := p.info.Selections[n]; sel != nil && sel.Obj() == valueField && !slices.Contains(readers, fn) {
				t.Errorf("%s: %s reads a secret value; only %v may", p.pos(n), fn, readers)
			}
		case *ast.CallExpr:
			f := p.callee(n)
			if f == nil || f.Pkg() == nil || !railsOutputPackages[f.Pkg().Path()] || slices.Contains(codecs, fn) {
				return
			}
			for _, arg := range n.Args {
				if railsHolds(p.info.TypeOf(arg), secretType, map[types.Type]bool{}) {
					t.Errorf("%s: %s passes secret values (%s) to %s.%s; only %v may serialize them",
						p.pos(n), fn, p.describe(arg), f.Pkg().Path(), f.Name(), codecs)
				}
			}
		}
	})
}

// docs/adr/0001: cmd.Stdout and cmd.Stderr of `ta run` are redactors over every
// environment that vault.load returned. TestRedactorAcrossWriteBoundaries
// covers what a redactor does with them.
func TestRailsRunRedactsEveryEnvironment(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "cmdRun")
	d := p.defs(fd)
	newRedactor := p.lookup(t, "newRedactor")
	isRedactor := func(src ast.Expr, stream string) bool {
		call, ok := src.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return false
		}
		if id, ok := ast.Unparen(call.Fun).(*ast.Ident); !ok || p.info.Uses[id] != newRedactor {
			return false
		}
		w, ok := ast.Unparen(call.Args[0]).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if obj := p.info.Uses[w.Sel]; obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != "os" || obj.Name() != stream {
			return false
		}
		envs := p.sources(d, call.Args[1], map[types.Object]bool{})
		for _, e := range envs {
			c, ok := e.(*ast.CallExpr)
			if !ok || !railsIs(p.callee(c), "main", "vault", "load") {
				return false
			}
		}
		return len(envs) > 0
	}
	set := map[string]bool{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr)
			if !ok {
				continue
			}
			s := p.info.Selections[sel]
			if s == nil || s.Kind() != types.FieldVal || s.Obj().Pkg() == nil || s.Obj().Pkg().Path() != "os/exec" {
				continue
			}
			stream := s.Obj().Name()
			if stream != "Stdout" && stream != "Stderr" {
				continue
			}
			set[stream] = true
			if len(as.Rhs) != len(as.Lhs) {
				t.Errorf("%s: cmd.%s is not assigned a single value", p.pos(as), stream)
				continue
			}
			for _, src := range p.sources(d, as.Rhs[i], map[types.Object]bool{}) {
				if !isRedactor(src, stream) {
					t.Errorf("%s: cmd.%s gets %s; want newRedactor(os.%s, envs) with envs straight from vault.load",
						p.pos(as), stream, p.describe(src), stream)
				}
			}
		}
		return true
	})
	for _, stream := range []string{"Stdout", "Stderr"} {
		if !set[stream] {
			t.Errorf("cmdRun never sets cmd.%s; the child's %s must go through a redactor", stream, strings.ToLower(stream))
		}
	}
}

// docs/adr/0004: cmdRun reads the vault's environments only to look up the -e
// environment, list environment names for an error, and build redactors.
func TestRailsRunUsesOnlyTheChosenEnvironment(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "cmdRun")
	envsType := p.lookup(t, "environments").Type()
	envFlag := p.field(t, "commandFlags", "env")
	newRedactor := p.lookup(t, "newRedactor")
	allowedArg := func(call *ast.CallExpr) bool {
		if id, ok := ast.Unparen(call.Fun).(*ast.Ident); ok {
			if b, ok := p.info.Uses[id].(*types.Builtin); ok && b.Name() == "len" {
				return true
			}
			if p.info.Uses[id] == newRedactor {
				return true
			}
		}
		return railsIs(p.callee(call), "maps", "", "Keys")
	}
	ast.PreorderStack(fd.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || p.info.Uses[id] == nil || !types.Identical(p.info.TypeOf(id), envsType) {
			return true
		}
		switch parent := stack[len(stack)-1].(type) {
		case *ast.IndexExpr:
			if parent.X == id {
				if sel, ok := ast.Unparen(parent.Index).(*ast.SelectorExpr); ok && p.info.Uses[sel.Sel] == envFlag {
					return true
				}
				t.Errorf("%s: cmdRun looks up environment %s; only the -e environment (flags.env) may be used",
					p.pos(parent), p.describe(parent.Index))
				return true
			}
		case *ast.CallExpr:
			if slices.Contains(parent.Args, ast.Expr(id)) && allowedArg(parent) {
				return true
			}
		}
		t.Errorf("%s: cmdRun uses the vault's environments in %s; it may only index them with flags.env, pass them to len, maps.Keys or newRedactor",
			p.pos(id), p.describe(stack[len(stack)-1]))
		return true
	})
}

// docs/adr/0001: `ta set` stores only what readSecretValue read (hidden prompt
// or stdin), the web UI only the decoded request body; nothing else stores values.
func TestRailsValuesEnterTheVaultOnlyFromPromptStdinOrRequestBody(t *testing.T) {
	p := railsLoad(t)
	readSecretValue := p.lookup(t, "readSecretValue")
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		call, ok := n.(*ast.CallExpr)
		if !ok || !railsIs(p.callee(call), "main", "vault", "set") {
			return
		}
		fn := railsName(fd)
		if len(call.Args) != 3 {
			t.Errorf("%s: vault.set called with %d arguments; this test expects (env, name, value)", p.pos(call), len(call.Args))
			return
		}
		value := call.Args[2]
		switch fn {
		case "cmdSet":
			for _, src := range p.sources(p.defs(fd), value, map[types.Object]bool{}) {
				c, ok := src.(*ast.CallExpr)
				if !ok || p.callee(c) != readSecretValue {
					t.Errorf("%s: ta set stores %s; the value must come only from readSecretValue, never from arguments or flags",
						p.pos(call), p.describe(src))
				}
			}
		case "server.put":
			if !p.decodedFromRequestBody(fd, value) {
				t.Errorf("%s: the web UI stores %s; the value must be a field of the JSON request body", p.pos(call), p.describe(value))
			}
		default:
			t.Errorf("%s: %s stores a secret value; only cmdSet and server.put may", p.pos(call), fn)
		}
	})
}

// decodedFromRequestBody reports whether e is body.Field where fd decodes the
// request body into body with encoding/json.
func (p *railsPackage) decodedFromRequestBody(fd *ast.FuncDecl, e ast.Expr) bool {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	base, ok := ast.Unparen(sel.X).(*ast.Ident)
	if !ok {
		return false
	}
	body := p.info.Uses[base]
	decoded := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !railsIs(p.callee(call), "encoding/json", "Decoder", "Decode") || len(call.Args) != 1 {
			return true
		}
		addr, ok := ast.Unparen(call.Args[0]).(*ast.UnaryExpr)
		if !ok || addr.Op != token.AND {
			return true
		}
		if id, ok := ast.Unparen(addr.X).(*ast.Ident); !ok || p.info.Uses[id] != body {
			return true
		}
		ast.Inspect(call.Fun, func(m ast.Node) bool {
			if s, ok := m.(*ast.SelectorExpr); ok {
				if f := p.info.Selections[s]; f != nil && f.Kind() == types.FieldVal && f.Obj().Name() == "Body" &&
					f.Obj().Pkg() != nil && f.Obj().Pkg().Path() == "net/http" {
					decoded = true
				}
			}
			return true
		})
		return true
	})
	return decoded
}

// docs/adr/0001: no argument of a child process is built from bytes, which is
// how the vault key exists in memory.
func TestRailsKeychainKeyIsNeverAProcessArgument(t *testing.T) {
	p := railsLoad(t)
	byteSlice := types.NewSlice(types.Typ[types.Byte])
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		if f := p.callee(call); !railsIs(f, "os/exec", "", "Command") && !railsIs(f, "os/exec", "", "CommandContext") {
			return
		}
		for _, arg := range call.Args {
			ast.Inspect(arg, func(m ast.Node) bool {
				e, ok := m.(ast.Expr)
				if !ok {
					return true
				}
				if tv, ok := p.info.Types[e]; ok && !tv.IsType() && types.Identical(tv.Type, byteSlice) {
					t.Errorf("%s: %s builds a process argument from bytes (%s); pass key material on stdin",
						p.pos(e), railsName(fd), p.describe(e))
					return false
				}
				return true
			})
		}
	})
}

func railsKey() []byte {
	key := make([]byte, 32)
	rand.Read(key)
	return key
}

func railsVault(t *testing.T, key []byte) *vault {
	t.Helper()
	v, err := newVault(vaultLocation{dir: t.TempDir()}, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.save(environments{}); err != nil {
		t.Fatal(err)
	}
	return v
}

type railsEnvelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func railsReadEnvelope(t *testing.T, dir string) ([]byte, railsEnvelope) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, vaultFile))
	if err != nil {
		t.Fatal(err)
	}
	var env railsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("vault file is not a JSON envelope: %v", err)
	}
	return raw, env
}

// railsSealedVault builds a vault file the way released versions wrote it,
// independent of this package's types.
func railsSealedVault(t *testing.T, key []byte, version int, plain string) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	raw, err := json.Marshal(railsEnvelope{
		Version:    version,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, []byte(plain), fmt.Appendf(nil, "tantalum vault v%d", version)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// docs/adr/0001: names, values and update times are only inside AES-256-GCM
// ciphertext, with a fresh nonce on every write.
func TestRailsVaultFileHoldsOnlyCiphertext(t *testing.T) {
	if keySize != 32 {
		t.Fatalf("keySize = %d bytes; the vault key must be 256-bit", keySize)
	}
	key := railsKey()
	v := railsVault(t, key)
	const env, name, value = "rails_env", "RAILS_NAME", "rails-secret-value"
	if err := v.set(env, name, value); err != nil {
		t.Fatal(err)
	}
	raw, first := railsReadEnvelope(t, v.dir)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(fields)); !slices.Equal(got, []string{"ciphertext", "nonce", "version"}) {
		t.Errorf("vault file fields = %v, want only ciphertext, nonce and version", got)
	}
	for _, s := range []string{env, name, value} {
		if bytes.Contains(raw, []byte(s)) || bytes.Contains(first.Ciphertext, []byte(s)) {
			t.Errorf("vault file contains %q in the clear", s)
		}
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aead.Open(nil, first.Nonce, first.Ciphertext, vaultAAD(first.Version))
	if err != nil {
		t.Fatalf("AES-256-GCM with the vault key cannot open the vault file: %v", err)
	}
	if !bytes.Contains(plain, []byte(value)) {
		t.Errorf("decrypted vault does not hold the stored value")
	}

	if err := v.set(env, name, value); err != nil {
		t.Fatal(err)
	}
	if _, second := railsReadEnvelope(t, v.dir); bytes.Equal(first.Nonce, second.Nonce) {
		t.Errorf("two writes used the same nonce %x; every write needs a fresh random nonce", first.Nonce)
	}
}

// docs/adr/0004: files written by every released format load, and the next
// write produces the current version without losing anything.
func TestRailsVaultLoadsEveryFormatItWrote(t *testing.T) {
	key := railsKey()
	updated := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		version int
		plain   string
		want    map[string]map[string]string
	}{
		{"version 1", 1,
			`{"secrets":{"OLD":{"value":"v1-value","updated":"2026-10-01T00:00:00Z"}}}`,
			map[string]map[string]string{"default": {"OLD": "v1-value"}}},
		{"version 2", 2,
			`{"envs":{"dev":{"API_KEY":{"value":"dev-value","updated":"2026-10-01T00:00:00Z"}},"prod":{"API_KEY":{"value":"prod-value","updated":"2026-10-01T00:00:00Z"}}}}`,
			map[string]map[string]string{"dev": {"API_KEY": "dev-value"}, "prod": {"API_KEY": "prod-value"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, vaultFile), railsSealedVault(t, key, tc.version, tc.plain), 0o600); err != nil {
				t.Fatal(err)
			}
			v, err := newVault(vaultLocation{dir: dir}, key)
			if err != nil {
				t.Fatal(err)
			}
			check := func(when string, envs environments) {
				for env, secrets := range tc.want {
					for name, value := range secrets {
						if s := envs[env][name]; s.Value != value || !s.Updated.Equal(updated) {
							t.Errorf("%s: %s/%s = %q updated %v, want %q updated %v", when, env, name, s.Value, s.Updated, value, updated)
						}
					}
				}
			}
			envs, err := v.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			check("load", envs)

			if err := v.set("rails", "NEW", "new-value"); err != nil {
				t.Fatalf("set: %v", err)
			}
			if _, env := railsReadEnvelope(t, dir); env.Version != vaultVersion {
				t.Errorf("rewritten vault has version %d, want the current %d", env.Version, vaultVersion)
			}
			if envs, err = v.load(); err != nil {
				t.Fatalf("load after rewrite: %v", err)
			}
			check("after rewrite", envs)
			if envs["rails"]["NEW"].Value != "new-value" {
				t.Errorf("after rewrite: rails/NEW missing")
			}
		})
	}
}

// docs/adr/0004: an unknown version, or a version changed in the file, stops
// every command and leaves the file as it was.
func TestRailsUnknownVaultVersionIsNeverOverwritten(t *testing.T) {
	key := railsKey()
	refuses := func(t *testing.T, dir string) {
		t.Helper()
		before, err := os.ReadFile(filepath.Join(dir, vaultFile))
		if err != nil {
			t.Fatal(err)
		}
		v, err := newVault(vaultLocation{dir: dir}, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.load(); err == nil {
			t.Error("load succeeded")
		}
		if err := v.set("dev", "X", "v"); err == nil {
			t.Error("set succeeded")
		}
		if err := v.remove("dev", "X"); err == nil {
			t.Error("remove succeeded")
		}
		if after, _ := os.ReadFile(filepath.Join(dir, vaultFile)); !bytes.Equal(before, after) {
			t.Error("the vault file was rewritten")
		}
	}

	t.Run("future version", func(t *testing.T) {
		dir := t.TempDir()
		raw := railsSealedVault(t, key, vaultVersion+1, `{"envs":{"dev":{"X":{"value":"v"}}}}`)
		if err := os.WriteFile(filepath.Join(dir, vaultFile), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		refuses(t, dir)
	})

	t.Run("version changed in the file", func(t *testing.T) {
		v := railsVault(t, key)
		if err := v.set("dev", "KEEP", "keep-value"); err != nil {
			t.Fatal(err)
		}
		_, env := railsReadEnvelope(t, v.dir)
		for _, version := range []int{1, vaultVersion + 1} {
			if version == env.Version {
				continue
			}
			forged := env
			forged.Version = version
			raw, err := json.Marshal(forged)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(v.dir, vaultFile), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			refuses(t, v.dir)
		}
	})
}

// docs/adr/0004
func TestRailsEnvironmentExistsOnlyWhileItHoldsSecrets(t *testing.T) {
	v := railsVault(t, railsKey())
	for _, s := range [][2]string{{"staging", "A"}, {"staging", "B"}, {"dev", "C"}} {
		if err := v.set(s[0], s[1], "value-"+s[1]); err != nil {
			t.Fatal(err)
		}
	}
	envNames := func() []string {
		envs, err := v.load()
		if err != nil {
			t.Fatal(err)
		}
		return slices.Sorted(maps.Keys(envs))
	}
	if err := v.remove("staging", "A"); err != nil {
		t.Fatal(err)
	}
	if got := envNames(); !slices.Equal(got, []string{"dev", "staging"}) {
		t.Fatalf("after removing staging/A: environments %v, want [dev staging]", got)
	}
	if err := v.remove("staging", "B"); err != nil {
		t.Fatal(err)
	}
	if got := envNames(); !slices.Equal(got, []string{"dev"}) {
		t.Fatalf("after removing the last secret of staging: environments %v, want [dev]", got)
	}
}

const (
	railsHost  = "127.0.0.1:7340"
	railsToken = "rails-token"
)

type railsRequest struct{ method, path, body string }

// railsRoutes is every static file of the page plus every API route.
func railsRoutes(t *testing.T) []railsRequest {
	t.Helper()
	routes := []railsRequest{{"GET", "/", ""}}
	err := fs.WalkDir(webFiles, "web", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			routes = append(routes, railsRequest{"GET", strings.TrimPrefix(path, "web"), ""})
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(routes,
		railsRequest{"GET", "/api/vault", ""},
		railsRequest{"GET", "/api/secrets", ""},
		railsRequest{"PUT", "/api/secrets/dev/RAILS", `{"value":"v"}`},
		railsRequest{"DELETE", "/api/secrets/dev/RAILS", ""},
		railsRequest{"POST", "/api/envs/dev/copy", `{"to":"rails-copy"}`},
		railsRequest{"POST", "/api/envs/rails-copy/rename", `{"to":"rails-renamed"}`},
		railsRequest{"DELETE", "/api/envs/rails-renamed", ""},
		railsRequest{"POST", "/api/names/API_KEY/rename", `{"to":"RAILS_KEY"}`},
		railsRequest{"DELETE", "/api/names/RAILS_KEY", ""},
	)
}

func railsServe(h http.Handler, r railsRequest, host string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(r.method, "http://"+railsHost+r.path, strings.NewReader(r.body))
	req.Host = host
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// docs/adr/0003
func TestRailsServeRefusesNonLoopbackAddresses(t *testing.T) {
	t.Setenv("TANTALUM_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	// A loopback address passes the address check and then fails: there is no vault.
	noVault := cmdServe([]string{"-addr", "127.0.0.1:0"})
	if noVault == nil {
		t.Fatal("ta serve started without a vault")
	}
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0", "192.0.2.1:0", "example.com:0"} {
		if err := cmdServe([]string{"-addr", addr}); err == nil || err.Error() == noVault.Error() {
			t.Errorf("-addr %s: got %v; want a refusal before the vault is opened", addr, err)
		}
	}
}

// docs/adr/0003
func TestRailsNoResponseContainsASecretValue(t *testing.T) {
	v := railsVault(t, railsKey())
	values := []string{"rails-dev-value-1", "rails-prod-value-2"}
	if err := v.set("dev", "API_KEY", values[0]); err != nil {
		t.Fatal(err)
	}
	if err := v.set("prod", "API_KEY", values[1]); err != nil {
		t.Fatal(err)
	}
	h := newServer(v, railsToken, []string{railsHost})
	auth := map[string]string{"Authorization": "Bearer " + railsToken}
	requests := append(railsRoutes(t),
		railsRequest{"GET", "/api/secrets/dev/API_KEY", ""},
		railsRequest{"GET", "/api/secrets/dev", ""},
		railsRequest{"GET", "/api/secrets/dev/API_KEY/value", ""},
		railsRequest{"GET", "/api/secrets?reveal=1&values=true", ""},
		railsRequest{"HEAD", "/api/secrets", ""},
		railsRequest{"OPTIONS", "/api/secrets/dev/API_KEY", ""},
		railsRequest{"POST", "/api/secrets/dev/API_KEY", ""},
		railsRequest{"PUT", "/api/secrets/dev/API_KEY", `{"value":"` + values[0] + `"}`},
		railsRequest{"PUT", "/api/secrets/dev/9BAD", `{"value":"` + values[0] + `"}`},
		railsRequest{"PUT", "/api/secrets/dev/API_KEY", `{"value":"` + values[0] + `", "bad json`},
		railsRequest{"DELETE", "/api/secrets/prod/API_KEY", ""},
		railsRequest{"GET", "/api/secrets", ""},
	)
	for _, r := range requests {
		rec := railsServe(h, r, railsHost, auth)
		var headers bytes.Buffer
		rec.Header().Write(&headers)
		for _, value := range values {
			if strings.Contains(rec.Body.String(), value) || strings.Contains(headers.String(), value) {
				t.Errorf("%s %s: response (%d) contains a secret value", r.method, r.path, rec.Code)
			}
		}
	}
}

// docs/adr/0003
func TestRailsServeGuardsEveryRoute(t *testing.T) {
	h := newServer(railsVault(t, railsKey()), railsToken, []string{railsHost})
	auth := "Bearer " + railsToken
	type guardCase struct {
		name   string
		host   string
		header map[string]string
		want   int
	}
	for _, r := range railsRoutes(t) {
		cases := []guardCase{
			{"foreign Host (DNS rebinding)", "evil.example:7340", map[string]string{"Authorization": auth}, http.StatusForbidden},
			{"foreign Origin", railsHost, map[string]string{"Authorization": auth, "Origin": "http://evil.example"}, http.StatusForbidden},
			{"https Origin", railsHost, map[string]string{"Authorization": auth, "Origin": "https://" + railsHost}, http.StatusForbidden},
			{"opaque Origin", railsHost, map[string]string{"Authorization": auth, "Origin": "null"}, http.StatusForbidden},
		}
		if strings.HasPrefix(r.path, "/api/") {
			cases = append(cases,
				guardCase{"no token", railsHost, nil, http.StatusUnauthorized},
				guardCase{"wrong token", railsHost, map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
			)
		}
		for _, c := range cases {
			if rec := railsServe(h, r, c.host, c.header); rec.Code != c.want {
				t.Errorf("%s %s with %s: status %d, want %d", r.method, r.path, c.name, rec.Code, c.want)
			}
		}
	}
}

// docs/adr/0003
func TestRailsServeSendsStrictContentSecurityPolicy(t *testing.T) {
	h := newServer(railsVault(t, railsKey()), railsToken, []string{railsHost})
	type probe struct {
		railsRequest
		header map[string]string
	}
	auth := map[string]string{"Authorization": "Bearer " + railsToken}
	var probes []probe
	for _, r := range railsRoutes(t) {
		probes = append(probes, probe{r, auth})
	}
	probes = append(probes,
		probe{railsRequest{"GET", "/no-such-file", ""}, auth},
		probe{railsRequest{"GET", "/api/secrets", ""}, nil},
	)
	for _, pr := range probes {
		rec := railsServe(h, pr.railsRequest, railsHost, pr.header)
		csp := rec.Header().Get("Content-Security-Policy")
		directives := map[string][]string{}
		for d := range strings.SplitSeq(csp, ";") {
			if f := strings.Fields(d); len(f) > 0 {
				directives[strings.ToLower(f[0])] = f[1:]
			}
		}
		where := fmt.Sprintf("%s %s (%d): Content-Security-Policy %q", pr.method, pr.path, rec.Code, csp)
		if _, ok := directives["default-src"]; !ok {
			t.Errorf("%s: no default-src", where)
		}
		for name, sources := range directives {
			if strings.HasSuffix(name, "-src") {
				for _, s := range sources {
					if s != "'self'" && s != "'none'" {
						t.Errorf("%s: %s allows %s; only 'self' or 'none'", where, name, s)
					}
				}
			}
		}
		if !slices.Equal(directives["frame-ancestors"], []string{"'none'"}) {
			t.Errorf("%s: want frame-ancestors 'none'", where)
		}
	}
}

// docs/adr/0002
func TestRailsStoredNamesKeepTantalum(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TANTALUM_HOME", "")
	t.Chdir(t.TempDir())
	if loc, err := locateVault(); err != nil || loc.dir != filepath.Join(home, ".tantalum") {
		t.Errorf("default vault = %q (%v), want ~/.tantalum (%s)", loc.dir, err, filepath.Join(home, ".tantalum"))
	}
	custom := t.TempDir()
	t.Setenv("TANTALUM_HOME", custom)
	if loc, err := locateVault(); err != nil || loc.dir != custom {
		t.Errorf("vault with TANTALUM_HOME=%s is %q (%v)", custom, loc.dir, err)
	}
	if keychainService != "tantalum" {
		t.Errorf("Keychain service = %q, want tantalum: existing vault keys are stored under it", keychainService)
	}
}

// docs/adr/0002: runs main in a child process, so os.Exit and stderr are real.
func TestRailsOwnErrorsArePrefixedTa(t *testing.T) {
	if args, ok := os.LookupEnv("TA_RAILS_MAIN_ARGS"); ok {
		os.Args = append([]string{"ta"}, strings.Fields(args)...)
		main()
		return
	}
	for _, args := range []string{"no-such-command", "set", "list", "run -s X", "serve -addr 0.0.0.0:0"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRailsOwnErrorsArePrefixedTa$")
		cmd.Env = append(os.Environ(), "TA_RAILS_MAIN_ARGS="+args, "TANTALUM_HOME="+t.TempDir())
		cmd.Dir = t.TempDir()
		cmd.Stdin = strings.NewReader("")
		cmd.Stdout = io.Discard
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err == nil {
			t.Errorf("ta %s: succeeded; want an error", args)
			continue
		}
		if !strings.HasPrefix(stderr.String(), "ta: ") {
			t.Errorf("ta %s: stderr %q; want it to start with \"ta: \"", args, stderr.String())
		}
	}
}

// docs/adr/0002
func TestRailsDocsInvokeTheCommandAsTa(t *testing.T) {
	oldName := regexp.MustCompile(`\btantalum\s+(init|set|list|rm|run|serve|help)\b`)
	bp, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{filepath.Join("..", "..", "README.md")}, bp.GoFiles...)
	for _, root := range []string{"web", filepath.Join("..", "..", "skills")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				paths = append(paths, path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if m := oldName.FindString(line); m != "" {
				t.Errorf("%s:%d: %q; the command is ta", path, i+1, m)
			}
		}
	}
}
