package main

// Architecture tests behind the rails in docs/adr/*.md. Each TestRails* test
// backs a `check:` in .krypton/rails.toml, which runs it by name with
// `go test -run`; a renamed test makes its check match nothing and pass, so
// rename the check's argv with it.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	// The cgo files (Touch ID prompt, clipboard) are type-checked too; their
	// C.* references are accepted without a C toolchain.
	for _, name := range append(bp.GoFiles, bp.CgoFiles...) {
		f, err := parser.ParseFile(p.fset, name, nil, 0)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, f)
	}
	conf := types.Config{Importer: importer.ForCompiler(p.fset, "gc", nil), FakeImportC: true}
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
// redactor, to reveal it, or to check an opened export file; only the vault and
// export file codecs serialize value-holding types.
func TestRailsSecretValuesReachOnlyChildEnvAndRedactor(t *testing.T) {
	p := railsLoad(t)
	secretType := p.lookup(t, "secret").Type()
	valueField := p.field(t, "secret", "Value")
	readers := []string{"cmdRun", "newRedactor", "revealSecret", "openExport"}
	codecs := []string{"vault.load", "vault.save", "sealExport", "openExport"}
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
// environment that vault.load returned, or the child's end of a feed that such
// a redactor reads: redactor.terminal for a child on its own pseudo-terminal
// (which carries both streams, so it may be either stream's redactor), or
// redactor.pipe for a stream of that child redirected away from the terminal.
// TestRedactorAcrossWriteBoundaries covers what a redactor does with them, and
// TestTerminalFeedRedactsChildOutput what a terminal feed does.
func TestRailsRunRedactsEveryEnvironment(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "cmdRun")
	d := p.defs(fd)
	newRedactor := p.lookup(t, "newRedactor")
	childField := p.field(t, "feed", "child")
	// isRedactor reports whether src is newRedactor(os.<one of streams>, envs).
	isRedactor := func(src ast.Expr, streams ...string) bool {
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
		if obj := p.info.Uses[w.Sel]; obj == nil || obj.Pkg() == nil || obj.Pkg().Path() != "os" || !slices.Contains(streams, obj.Name()) {
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
	// isFeed reports whether src is f.child of a feed f that R.terminal or
	// R.pipe started, with R a redactor over stream (either stream for terminal).
	isFeed := func(src ast.Expr, stream string) bool {
		sel, ok := src.(*ast.SelectorExpr)
		if !ok || p.info.Selections[sel] == nil || p.info.Selections[sel].Obj() != childField {
			return false
		}
		feeds := p.sources(d, sel.X, map[types.Object]bool{})
		for _, f := range feeds {
			call, ok := f.(*ast.CallExpr)
			if !ok {
				return false
			}
			var streams []string
			switch fn := p.callee(call); {
			case railsIs(fn, "main", "redactor", "terminal"):
				streams = []string{"Stdout", "Stderr"}
			case railsIs(fn, "main", "redactor", "pipe"):
				streams = []string{stream}
			default:
				return false
			}
			recv := ast.Unparen(call.Fun).(*ast.SelectorExpr).X
			redactors := p.sources(d, recv, map[types.Object]bool{})
			for _, r := range redactors {
				if !isRedactor(r, streams...) {
					return false
				}
			}
			if len(redactors) == 0 {
				return false
			}
		}
		return len(feeds) > 0
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
				if !isRedactor(src, stream) && !isFeed(src, stream) {
					t.Errorf("%s: cmd.%s gets %s; want newRedactor(os.%s, envs) with envs straight from vault.load, "+
						"or the child's end of a feed that such a redactor's terminal or pipe method started",
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
// or stdin), import only a file/stdin reader's bytes (a .env or export file),
// and the web UI only the decoded request body. Keep this test's name in sync
// with its Krypton check.
func TestRailsValuesEnterTheVaultOnlyFromPromptStdinOrRequestBody(t *testing.T) {
	p := railsLoad(t)
	readSecretValue := p.lookup(t, "readSecretValue")
	readImportFile := p.lookup(t, "readImportFile")
	importers := map[string]int{"importDotenv": 1, "importExport": 0} // which argument holds the file bytes
	imports := map[string]int{}
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		fn := railsName(fd)
		for method, arg := range importers {
			if !railsIs(p.callee(call), "main", "vault", method) {
				continue
			}
			imports[method]++
			if fn != "cmdImport" || len(call.Args) != 3 {
				t.Errorf("%s: only cmdImport may import file/stdin bytes", p.pos(call))
				return
			}
			for _, src := range p.sources(p.defs(fd), call.Args[arg], map[types.Object]bool{}) {
				c, ok := src.(*ast.CallExpr)
				if !ok || p.callee(c) != readImportFile {
					t.Errorf("%s: import bytes must come from readImportFile, never arguments or flags", p.pos(call))
				}
			}
			return
		}
		if !railsIs(p.callee(call), "main", "vault", "set") {
			return
		}
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
	for method := range importers {
		if imports[method] != 1 {
			t.Errorf("found %d vault.%s calls, want one guarded cmdImport call", imports[method], method)
		}
	}
	reader := p.funcDecl(t, "readImportFile")
	path := p.info.Defs[reader.Type.Params.List[0].Names[0]]
	ast.Inspect(reader.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if len(ret.Results) == 1 {
			if call, ok := ret.Results[0].(*ast.CallExpr); ok && len(call.Args) == 1 {
				arg := call.Args[0]
				if railsIs(p.callee(call), "os", "", "ReadFile") {
					if id, ok := arg.(*ast.Ident); ok && p.info.Uses[id] == path {
						return true
					}
				}
				if railsIs(p.callee(call), "io", "", "ReadAll") {
					if sel, ok := arg.(*ast.SelectorExpr); ok {
						obj := p.info.Uses[sel.Sel]
						if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "os" && obj.Name() == "Stdin" {
							return true
						}
					}
				}
			}
		}
		t.Errorf("%s: import reader must return os.ReadFile(path) or io.ReadAll(os.Stdin)", p.pos(ret))
		return true
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
		if _, _, err := v.importDotenv("dev", []byte("X=new-value"), true); err == nil {
			t.Error("import succeeded")
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
		railsRequest{"POST", "/api/secrets/dev/API_KEY/reveal", ""},
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

// docs/adr/0003: also while the person at the Mac approves every reveal.
func TestRailsNoResponseContainsASecretValue(t *testing.T) {
	approved := 0
	stubReveal(t, func(string) error { approved++; return nil })
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
		railsRequest{"POST", "/api/secrets/dev/API_KEY/reveal?value=1", ""},
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
	if approved != 2 {
		t.Fatalf("%d reveals reached the prompt, want 2 (both of dev/API_KEY); otherwise the reveal probes test nothing", approved)
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
	for _, args := range []string{"no-such-command", "set", "import", "import input.env", "export", "export out.ta-export", "list", "run -s X", "serve -addr 0.0.0.0:0", "skill", "skill setup -agent nope"} {
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
	oldName := regexp.MustCompile(`\btantalum\s+(init|set|import|export|list|rm|run|reveal|serve|skill|help)\b`)
	bp, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string{filepath.Join("..", "..", "README.md")}, append(bp.GoFiles, bp.CgoFiles...)...)
	// The guides in docs/; the ADRs under docs/adr record decisions, which may
	// quote the old name.
	adrs := filepath.Join("..", "..", "docs", "adr")
	for _, root := range []string{"web", "skill", filepath.Join("..", "..", "skills"), filepath.Join("..", "..", "docs")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() && path == adrs {
				return filepath.SkipDir
			}
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

// docs/adr/0005: revealSecret reads a value only after confirmUser approved,
// and confirmUser is the system prompt rather than a stand-in.
func TestRailsRevealAsksBeforeReadingAValue(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "revealSecret")
	confirm := p.lookup(t, "confirmUser")
	valueField := p.field(t, "secret", "Value")

	// gate is the end of `if err := confirmUser(...); err != nil { ...; return ... }`.
	var gate token.Pos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || gate.IsValid() || len(ifs.Body.List) == 0 {
			return true
		}
		init, ok := ifs.Init.(*ast.AssignStmt)
		if !ok || len(init.Rhs) != 1 {
			return true
		}
		call, ok := ast.Unparen(init.Rhs[0]).(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := ast.Unparen(call.Fun).(*ast.Ident); !ok || p.info.Uses[id] != confirm {
			return true
		}
		if _, ok := ifs.Body.List[len(ifs.Body.List)-1].(*ast.ReturnStmt); ok {
			gate = ifs.End()
		}
		return true
	})
	if !gate.IsValid() {
		t.Fatal("revealSecret has no `if err := confirmUser(...); err != nil { return ... }`")
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if s := p.info.Selections[sel]; s != nil && s.Obj() == valueField && sel.Pos() < gate {
				t.Errorf("%s: revealSecret reads a value before confirmUser approved", p.pos(sel))
			}
		}
		return true
	})

	presence := p.lookup(t, "confirmUserPresence")
	wired := false
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || fd != nil {
			return
		}
		for i, name := range spec.Names {
			if p.info.Defs[name] != confirm {
				continue
			}
			if i < len(spec.Values) {
				id, ok := ast.Unparen(spec.Values[i]).(*ast.Ident)
				wired = ok && p.info.Uses[id] == presence
			}
		}
	})
	if !wired {
		t.Error("confirmUser is not initialised to confirmUserPresence, the system prompt")
	}
}

// docs/adr/0005: revealSecret hands the value only to fmt.Fprint* on a file
// opened from /dev/tty, or to copyToClipboard; never to stdout or stderr.
func TestRailsRevealWritesTheValueOnlyToTheTerminalOrClipboard(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "revealSecret")
	d := p.defs(fd)
	valueField := p.field(t, "secret", "Value")
	copyFn := p.lookup(t, "copyToClipboard")

	held := map[types.Object]bool{} // variables assigned a value
	for obj, defs := range d {
		for _, e := range defs {
			if sel, ok := ast.Unparen(e).(*ast.SelectorExpr); ok {
				if s := p.info.Selections[sel]; s != nil && s.Obj() == valueField {
					held[obj] = true
				}
			}
		}
	}
	if len(held) == 0 {
		t.Fatal("revealSecret never assigns a value to a variable; this test follows that variable")
	}
	openedTTY := func(e ast.Expr) bool {
		opened := false
		for _, src := range p.sources(d, e, map[types.Object]bool{}) {
			if src == nil {
				continue // the zero value of `var tty *os.File`
			}
			c, ok := src.(*ast.CallExpr)
			if !ok || !railsIs(p.callee(c), "os", "", "OpenFile") || len(c.Args) == 0 {
				return false
			}
			if lit, ok := ast.Unparen(c.Args[0]).(*ast.BasicLit); !ok || lit.Value != `"/dev/tty"` {
				return false
			}
			opened = true
		}
		return opened
	}
	ast.PreorderStack(fd.Body, nil, func(n ast.Node, stack []ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || !held[p.info.Uses[id]] {
			return true
		}
		if call, ok := stack[len(stack)-1].(*ast.CallExpr); ok && slices.Contains(call.Args, ast.Expr(id)) {
			f := p.callee(call)
			if f != nil && types.Object(f) == copyFn {
				return true
			}
			for _, name := range []string{"Fprint", "Fprintln", "Fprintf"} {
				if railsIs(f, "fmt", "", name) && call.Args[0] != ast.Expr(id) && openedTTY(call.Args[0]) {
					return true
				}
			}
		}
		t.Errorf("%s: revealSecret hands the value to %s; only a file opened from /dev/tty or copyToClipboard may get it",
			p.pos(id), p.describe(stack[len(stack)-1]))
		return true
	})
}

// docs/adr/0005: the clipboard is cleared only when nothing was copied after
// the revealed value. A private pasteboard keeps the real clipboard untouched.
func TestRailsRevealClipboardClearsOnlyWhenUnchanged(t *testing.T) {
	old := pasteboardName
	pasteboardName = fmt.Sprintf("dev.tantalum.rails.%d", os.Getpid())
	t.Cleanup(func() { pasteboardName = old })

	revealed, err := copyToClipboard("rails-revealed-value")
	if err != nil {
		t.Fatal(err)
	}
	later, err := copyToClipboard("copied-later")
	if err != nil {
		t.Fatal(err)
	}
	if clearClipboardIfUnchanged(revealed) {
		t.Error("cleared the clipboard although something was copied after the revealed value")
	}
	if !clearClipboardIfUnchanged(later) {
		t.Error("kept a value that nothing replaced")
	}
	if _, err := copyToClipboard("\xff\xfe not utf-8"); err == nil {
		t.Error("copied bytes that are not UTF-8 text")
	}
}

// railsTerminal is a pseudo-terminal on which lines are already typed, as a
// person would type passphrases at its prompts.
func railsTerminal(t *testing.T, lines ...string) *os.File {
	t.Helper()
	keyboard, tty, err := openPTY()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keyboard.Close(); tty.Close() })
	for _, line := range lines {
		if _, err := keyboard.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	return tty
}

// railsConfirm answers the Mac's prompt with answer for the rest of the test.
func railsConfirm(t *testing.T, answer func(reason string) error) {
	t.Helper()
	old := confirmUser
	t.Cleanup(func() { confirmUser = old })
	confirmUser = answer
}

const railsPassphrase = "rails passphrase 1"

// railsSealedExport builds an export file the way released versions wrote it,
// independent of this package's types.
func railsSealedExport(t *testing.T, passphrase string, version, iterations int, kdf, plain string) []byte {
	t.Helper()
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
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
	raw, err := json.Marshal(map[string]any{
		"format": "tantalum-export", "version": version, "kdf": kdf, "iterations": iterations,
		"salt": salt, "nonce": nonce,
		"ciphertext": aead.Seal(nil, nonce, []byte(plain), fmt.Appendf(nil, "tantalum-export v%d", version)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// docs/adr/0006: exportVault seals the vault only after confirmUser approved,
// and it is the only caller of sealExport; a refused prompt writes nothing.
func TestRailsExportAsksBeforeSealing(t *testing.T) {
	p := railsLoad(t)
	fd := p.funcDecl(t, "exportVault")
	confirm := p.lookup(t, "confirmUser")
	sealExport := p.lookup(t, "sealExport")

	var gate token.Pos
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || gate.IsValid() || len(ifs.Body.List) == 0 {
			return true
		}
		init, ok := ifs.Init.(*ast.AssignStmt)
		if !ok || len(init.Rhs) != 1 {
			return true
		}
		call, ok := ast.Unparen(init.Rhs[0]).(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := ast.Unparen(call.Fun).(*ast.Ident); !ok || p.info.Uses[id] != confirm {
			return true
		}
		if _, ok := ifs.Body.List[len(ifs.Body.List)-1].(*ast.ReturnStmt); ok {
			gate = ifs.End()
		}
		return true
	})
	if !gate.IsValid() {
		t.Fatal("exportVault has no `if err := confirmUser(...); err != nil { return ... }`")
	}
	sealed := 0
	p.inspect(func(n ast.Node, enclosing *ast.FuncDecl) {
		call, ok := n.(*ast.CallExpr)
		if !ok || p.callee(call) != sealExport {
			return
		}
		sealed++
		if enclosing != fd {
			t.Errorf("%s: %s seals an export file; only exportVault may, after the prompt", p.pos(call), railsName(enclosing))
		} else if call.Pos() < gate {
			t.Errorf("%s: exportVault seals the vault before confirmUser approved", p.pos(call))
		}
	})
	if sealed != 1 {
		t.Errorf("found %d sealExport calls, want exactly one in exportVault", sealed)
	}

	v := railsVault(t, railsKey())
	if err := v.set("dev", "RAILS", "rails-value"); err != nil {
		t.Fatal(err)
	}
	railsConfirm(t, func(string) error { return fmt.Errorf("Canceled by user.") })
	path := filepath.Join(t.TempDir(), "out.ta-export")
	if _, _, err := exportVault(v, path, railsTerminal(t, railsPassphrase, railsPassphrase)); err == nil {
		t.Fatal("export succeeded although the prompt was refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("a refused export left a file: %v", err)
	}
}

// docs/adr/0006: an export file holds only its format, version, key derivation
// settings, salt, nonce and ciphertext, sealed with a fresh salt and nonce
// under a key that only the passphrase opens.
func TestRailsExportFileHoldsOnlyCiphertext(t *testing.T) {
	const env, name, value = "rails_env", "RAILS_NAME", "rails-secret-value"
	envs := environments{env: {name: {Value: value, Updated: time.Now().UTC()}}}
	first, err := sealExport(envs, []byte(railsPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(first, &fields); err != nil {
		t.Fatalf("export file is not a JSON envelope: %v", err)
	}
	want := []string{"ciphertext", "format", "iterations", "kdf", "nonce", "salt", "version"}
	if got := slices.Sorted(maps.Keys(fields)); !slices.Equal(got, want) {
		t.Errorf("export file fields = %v, want %v", got, want)
	}
	for _, s := range []string{env, name, value, railsPassphrase} {
		if bytes.Contains(first, []byte(s)) {
			t.Errorf("export file contains %q in the clear", s)
		}
	}
	var head struct {
		Format     string `json:"format"`
		Version    int    `json:"version"`
		KDF        string `json:"kdf"`
		Iterations int    `json:"iterations"`
		Salt       []byte `json:"salt"`
		Nonce      []byte `json:"nonce"`
		Ciphertext []byte `json:"ciphertext"`
	}
	if err := json.Unmarshal(first, &head); err != nil {
		t.Fatal(err)
	}
	if head.Format != "tantalum-export" || head.KDF != "pbkdf2-sha256" || head.Iterations < 600_000 || len(head.Salt) < 16 {
		t.Errorf("export file settings %q v%d %s x%d, %d-byte salt; want tantalum-export, pbkdf2-sha256, at least 600000 iterations and a 16-byte salt",
			head.Format, head.Version, head.KDF, head.Iterations, len(head.Salt))
	}
	key, err := pbkdf2.Key(sha256.New, railsPassphrase, head.Salt, head.Iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aead.Open(nil, head.Nonce, head.Ciphertext, fmt.Appendf(nil, "tantalum-export v%d", head.Version))
	if err != nil {
		t.Fatalf("AES-256-GCM with the PBKDF2-SHA256 key of the passphrase cannot open the export file: %v", err)
	}
	if !bytes.Contains(plain, []byte(value)) {
		t.Error("decrypted export file does not hold the value")
	}
	if _, err := openExport(first, []byte(railsPassphrase+"x")); err == nil {
		t.Error("another passphrase opened the export file")
	}
	second, err := sealExport(envs, []byte(railsPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	var again struct{ Salt, Nonce []byte }
	if err := json.Unmarshal(second, &again); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(head.Salt, again.Salt) || bytes.Equal(head.Nonce, again.Nonce) {
		t.Error("two exports reused a salt or nonce; every export needs fresh random ones")
	}
}

// docs/adr/0006: files written by every released export version load.
func TestRailsExportFilesOfEveryVersionLoad(t *testing.T) {
	data := railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256",
		`{"envs":{"dev":{"API_KEY":{"value":"dev-value","updated":"2026-10-01T00:00:00Z"}}}}`)
	envs, err := openExport(data, []byte(railsPassphrase))
	if err != nil {
		t.Fatalf("version 1: %v", err)
	}
	updated := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if s := envs["dev"]["API_KEY"]; s.Value != "dev-value" || !s.Updated.Equal(updated) {
		t.Errorf("version 1: dev/API_KEY = %q updated %v, want dev-value updated %v", s.Value, s.Updated, updated)
	}
}

// docs/adr/0006: a passphrase reaches sealExport and vault.importExport only
// from readHidden on the terminal that openTerminal opened from /dev/tty, never
// from an argument, a flag, the environment or stdin.
func TestRailsExportPassphraseComesOnlyFromTheTerminal(t *testing.T) {
	p := railsLoad(t)
	openTerm := p.lookup(t, "openTerminal")
	readHidden := p.lookup(t, "readHidden")
	exportFd := p.funcDecl(t, "exportVault")
	exportTTY := p.info.Defs[exportFd.Type.Params.List[len(exportFd.Type.Params.List)-1].Names[0]]

	// fromTerminal reports whether every source of e is openTerminal() or,
	// inside exportVault, its tty parameter.
	fromTerminal := func(d railsDefs, fd *ast.FuncDecl, e ast.Expr) bool {
		srcs := p.sources(d, e, map[types.Object]bool{})
		for _, src := range srcs {
			if id, ok := src.(*ast.Ident); ok && fd == exportFd && p.info.Uses[id] == exportTTY {
				continue
			}
			c, ok := src.(*ast.CallExpr)
			if !ok {
				return false
			}
			if id, ok := ast.Unparen(c.Fun).(*ast.Ident); !ok || p.info.Uses[id] != openTerm {
				return false
			}
		}
		return len(srcs) > 0
	}
	// typed reports whether every source of e is readHidden on the terminal.
	typed := func(d railsDefs, fd *ast.FuncDecl, e ast.Expr) bool {
		srcs := p.sources(d, e, map[types.Object]bool{})
		for _, src := range srcs {
			c, ok := src.(*ast.CallExpr)
			if !ok || p.callee(c) != readHidden || !fromTerminal(d, fd, c.Args[0]) {
				return false
			}
		}
		return len(srcs) > 0
	}
	checks := []struct {
		pkg, recv, name string
		arg             int
		check           func(railsDefs, *ast.FuncDecl, ast.Expr) bool
		want            string
	}{
		{"main", "", "sealExport", 1, typed, "readHidden on the terminal"},
		{"main", "vault", "importExport", 1, typed, "readHidden on the terminal"},
		{"main", "", "exportVault", 2, fromTerminal, "openTerminal()"},
	}
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return
		}
		for _, c := range checks {
			if railsIs(p.callee(call), c.pkg, c.recv, c.name) && !c.check(p.defs(fd), fd, call.Args[c.arg]) {
				t.Errorf("%s: %s passes %s to %s; it must come from %s", p.pos(call), railsName(fd), p.describe(call.Args[c.arg]), c.name, c.want)
			}
		}
	})

	opensTTY := false
	p.inspect(func(n ast.Node, fd *ast.FuncDecl) {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || fd != nil {
			return
		}
		for i, name := range spec.Names {
			if p.info.Defs[name] != openTerm || i >= len(spec.Values) {
				continue
			}
			ast.Inspect(spec.Values[i], func(m ast.Node) bool {
				c, ok := m.(*ast.CallExpr)
				if ok && railsIs(p.callee(c), "os", "", "OpenFile") && len(c.Args) > 0 {
					if lit, ok := ast.Unparen(c.Args[0]).(*ast.BasicLit); ok && lit.Value == `"/dev/tty"` {
						opensTTY = true
					}
				}
				return true
			})
		}
	})
	if !opensTTY {
		t.Error("openTerminal is not initialised to open /dev/tty")
	}
}

// docs/adr/0006: ta export creates a new file readable only by its owner and
// never replaces one, and never writes to stdout.
func TestRailsExportNeverReplacesAFile(t *testing.T) {
	if err := cmdExport([]string{"-"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("ta export - = %v, want a usage error: an export never goes to stdout", err)
	}
	v := railsVault(t, railsKey())
	if err := v.set("dev", "RAILS", "rails-value"); err != nil {
		t.Fatal(err)
	}
	railsConfirm(t, func(string) error { return nil })
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdExport([]string{existing}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("ta export onto an existing file = %v, want a refusal before anything else", err)
	}
	if _, _, err := exportVault(v, existing, railsTerminal(t, railsPassphrase, railsPassphrase)); err == nil {
		t.Error("exportVault replaced an existing file")
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep me" {
		t.Errorf("existing file now holds %q", data)
	}
	created := filepath.Join(dir, "new.ta-export")
	if _, _, err := exportVault(v, created, railsTerminal(t, railsPassphrase, railsPassphrase)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(created); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("export file mode = %v (%v), want 0600", info, err)
	}
}

// docs/adr/0006: importing an export file checks all of it before one locked
// write; a wrong passphrase, a damaged file, unknown settings or an invalid
// entry import nothing, and errors name no secret.
func TestRailsImportExportValidatesEverythingBeforeOneWrite(t *testing.T) {
	good := `{"envs":{"dev":{"OK":{"value":"private-value","updated":"2026-10-01T00:00:00Z"}}}}`
	var fields map[string]any
	if err := json.Unmarshal(railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256", good), &fields); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(fields["ciphertext"].(string))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[0] ^= 1
	fields["ciphertext"] = ciphertext
	tampered, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		data       []byte
		passphrase string
	}{
		{"wrong passphrase", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256", good), railsPassphrase + "x"},
		{"damaged file", tampered, railsPassphrase},
		{"future version", railsSealedExport(t, railsPassphrase, 2, 600_000, "pbkdf2-sha256", good), railsPassphrase},
		{"too few iterations", railsSealedExport(t, railsPassphrase, 1, 1000, "pbkdf2-sha256", good), railsPassphrase},
		{"unknown kdf", railsSealedExport(t, railsPassphrase, 1, 600_000, "md5", good), railsPassphrase},
		{"invalid name", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256",
			`{"envs":{"dev":{"OK":{"value":"private-value"},"9PRIVATE":{"value":"private-value"}}}}`), railsPassphrase},
		{"empty value", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256",
			`{"envs":{"dev":{"OK":{"value":"private-value"},"PRIVATE_EMPTY":{"value":""}}}}`), railsPassphrase},
		{"invalid environment", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256",
			`{"envs":{"dev":{"OK":{"value":"private-value"}},"private/env":{"OK":{"value":"private-value"}}}}`), railsPassphrase},
		{"empty environment", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256",
			`{"envs":{"dev":{"OK":{"value":"private-value"}},"private-env":{}}}`), railsPassphrase},
		{"no secrets", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256", `{"envs":{}}`), railsPassphrase},
		{"not JSON inside", railsSealedExport(t, railsPassphrase, 1, 600_000, "pbkdf2-sha256", `private-value`), railsPassphrase},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := railsVault(t, railsKey())
			if err := v.set("dev", "KEEP", "keep-value"); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(v.dir, vaultFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, overwrite := range []bool{false, true} {
				_, _, _, err := v.importExport(tc.data, []byte(tc.passphrase), overwrite)
				if err == nil {
					t.Fatal("import succeeded")
				}
				for _, s := range []string{"private", "PRIVATE"} {
					if strings.Contains(err.Error(), s) {
						t.Errorf("error names a secret: %v", err)
					}
				}
			}
			if after, _ := os.ReadFile(filepath.Join(v.dir, vaultFile)); !bytes.Equal(before, after) {
				t.Error("a rejected export file changed the vault")
			}
		})
	}
}
