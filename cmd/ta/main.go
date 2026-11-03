// Command ta (tantalum) is a local secret manager for AI agents. Secrets live in
// encrypted vaults (a default one, plus one per git repository, keyed by its
// directory) whose keys are kept in the macOS login Keychain, grouped into
// environments such as dev and prod. Agents never read values: they run commands
// with secrets passed as variables, and every value of the vault in use is
// redacted from the command's output.
package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// exitToolError is returned when ta itself fails, so it cannot be
// mistaken for an exit code of the wrapped command (same convention as env(1)).
const exitToolError = 125

// defaultEnv is the environment used when -e is not given.
const defaultEnv = "default"

const usage = `ta (tantalum) - local secret manager for AI agents

usage:
  ta init                 create the vault for this directory and store its key in
                          the login Keychain
  ta set [-e ENV] NAME    store a secret (hidden prompt, or stdin when piped)
  ta import [-e ENV] [-overwrite] FILE
                          import .env secrets from FILE, or - for stdin; keeps
                          existing secrets unless -overwrite is given;
                          invalid or empty entries fail without storing any
  ta list                 list environments and secret names, never values
  ta rm [-e ENV] NAME     delete a secret
  ta run [-e ENV] -s SPEC... -- COMMAND [ARGS...]
                          run COMMAND with secrets of ENV as variables; every
                          vault value is redacted from its stdout and stderr,
                          which are a terminal of its own when ta runs in one.
                          SPEC is NAME (variable NAME) or VAR=NAME (variable VAR)
  ta reveal [-e ENV] [-c] NAME
                          for humans: after Touch ID (or an Apple Watch or the
                          login password) shows the value on the terminal, or
                          with -c copies it to the clipboard and clears the
                          clipboard after 45 seconds; never writes it to stdout
  ta serve [-addr HOST:PORT]
                          web UI for humans to add, replace, import, copy,
                          rename and delete secrets, and to put a value on the
                          clipboard after Touch ID as reveal -c does (loopback
                          only; default 127.0.0.1:7340, or a free port when
                          that is taken); open the printed URL, which carries
                          this run's access token

-e ENV picks an environment: a named set of secrets such as dev or prod
(default "default"). An environment exists while it holds secrets.

Inside a git repository, every command uses that repository's own vault,
$TANTALUM_HOME/projects/NAME-HASH, keyed by the repository directory (all
worktrees share it). Elsewhere commands use the default vault in $TANTALUM_HOME.

configuration:
  TANTALUM_HOME           home of the default vault and projects/ (default ~/.tantalum)

exit status: ta's own errors exit 125; run exits with COMMAND's status
(126 not executable, 127 not found, 128+N killed by signal N).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(exitToolError)
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(args)
	case "set":
		err = cmdSet(args)
	case "import":
		err = cmdImport(args)
	case "list":
		err = cmdList(args)
	case "rm":
		err = cmdRm(args)
	case "run":
		var code int
		if code, err = cmdRun(args); err == nil {
			os.Exit(code)
		}
	case "serve":
		err = cmdServe(args)
	case "reveal":
		err = cmdReveal(args)
	case clearClipboardCommand:
		err = cmdClearClipboard(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		err = fmt.Errorf("unknown command %q (see `ta help`)", os.Args[1])
	}
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(usage)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ta: %v\n", err)
		os.Exit(exitToolError)
	}
}

// commandFlags is a subcommand's flag set with the shared -e ENV flag.
type commandFlags struct {
	*flag.FlagSet
	env string
}

func newCommandFlags(name string) *commandFlags {
	f := &commandFlags{FlagSet: flag.NewFlagSet(name, flag.ContinueOnError)}
	f.SetOutput(io.Discard)
	f.StringVar(&f.env, "e", defaultEnv, "")
	return f
}

func (f *commandFlags) parse(args []string) error {
	if err := f.Parse(args); err != nil {
		return err
	}
	if !validSlug(f.env) {
		return fmt.Errorf("invalid environment %q: use letters, digits, _ and -, starting with a letter or digit", f.env)
	}
	return nil
}

func cmdInit(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: ta init")
	}
	loc, err := locateVault()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(loc.dir, vaultFile)); err == nil {
		return fmt.Errorf("vault already exists in %s", loc)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := keychainGet(loc.dir); err == nil {
		return fmt.Errorf("a Keychain key for %s already exists without a vault; remove it first with:\n  security delete-generic-password -s %s -a '%s'", loc.dir, keychainService, loc.dir)
	} else if !errors.Is(err, errNoKey) {
		return err
	}
	if err := os.MkdirAll(loc.dir, 0o700); err != nil {
		return err
	}
	key := make([]byte, keySize)
	rand.Read(key)
	if err := keychainAdd(loc.dir, key); err != nil {
		return err
	}
	v, err := newVault(loc, key)
	if err != nil {
		return err
	}
	if err := v.save(environments{}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "initialized vault in %s; its key is in the login Keychain (service %q)\n", loc, keychainService)
	return nil
}

func cmdSet(args []string) error {
	flags := newCommandFlags("set")
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: ta set [-e ENV] NAME   (value from hidden prompt or stdin, never from arguments)")
	}
	name := flags.Arg(0)
	if !validName(name) {
		return fmt.Errorf("invalid name %q: use letters, digits and _, not starting with a digit", name)
	}
	// Open first so a missing vault fails before the human types the secret.
	v, err := openVault()
	if err != nil {
		return err
	}
	value, err := readSecretValue(flags.env + "/" + name)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		return errors.New("empty value; nothing stored")
	}
	if err := v.set(flags.env, name, string(value)); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored %s/%s (%d bytes)\n", flags.env, name, len(value))
	return nil
}

// readSecretValue reads from a hidden prompt when stdin is a terminal,
// otherwise all of stdin minus one trailing newline (so `echo x |` works).
func readSecretValue(label string) ([]byte, error) {
	if isTerminal(os.Stdin) {
		return readHidden(os.Stdin, fmt.Sprintf("value for %s (input hidden): ", label))
	}
	value, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	if !bytes.HasSuffix(value, []byte("\r\n")) {
		return bytes.TrimSuffix(value, []byte("\n")), nil
	}
	return value[:len(value)-2], nil
}

func cmdList(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: ta list")
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	envs, err := v.load()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "vault: %s\n", v.vaultLocation)
	if len(envs) == 0 {
		fmt.Fprintln(os.Stderr, "vault is empty; add a secret with `ta set [-e ENV] NAME`")
		return nil
	}
	for _, env := range slices.Sorted(maps.Keys(envs)) {
		for _, name := range slices.Sorted(maps.Keys(envs[env])) {
			fmt.Printf("%s\t%s\t%s\n", env, name, envs[env][name].Updated.Format(time.RFC3339))
		}
	}
	return nil
}

func cmdRm(args []string) error {
	flags := newCommandFlags("rm")
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: ta rm [-e ENV] NAME")
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	return v.remove(flags.env, flags.Arg(0))
}

type specList []string

func (l *specList) String() string     { return strings.Join(*l, ",") }
func (l *specList) Set(s string) error { *l = append(*l, s); return nil }

func cmdRun(args []string) (int, error) {
	flags := newCommandFlags("run")
	var specs specList
	flags.Var(&specs, "s", "")
	if err := flags.parse(args); err != nil {
		return 0, err
	}
	argv := flags.Args()
	if len(argv) == 0 || len(specs) == 0 {
		return 0, errors.New("usage: ta run [-e ENV] -s NAME [-s VAR=NAME]... -- COMMAND [ARGS...]")
	}

	v, err := openVault()
	if err != nil {
		return 0, err
	}
	envs, err := v.load()
	if err != nil {
		return 0, err
	}
	secrets, ok := envs[flags.env]
	if !ok {
		have := "none"
		if len(envs) > 0 {
			have = strings.Join(slices.Sorted(maps.Keys(envs)), ", ")
		}
		return 0, fmt.Errorf("no environment %q in the vault (environments: %s)", flags.env, have)
	}
	childEnv := os.Environ()
	var missing []string
	for _, spec := range specs {
		variable, name, mapped := strings.Cut(spec, "=")
		if !mapped {
			name = variable
		}
		if !validName(variable) || !validName(name) {
			return 0, fmt.Errorf("invalid -s %q: want NAME or VAR=NAME", spec)
		}
		s, ok := secrets[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		// exec keeps the last value of a duplicated key, so this overrides any inherited variable.
		childEnv = append(childEnv, variable+"="+s.Value)
	}
	if len(missing) > 0 {
		return 0, fmt.Errorf("not in environment %q: %s (a human adds them with `ta set -e %s NAME` or `ta serve`)",
			flags.env, strings.Join(missing, ", "), flags.env)
	}

	// Redact every environment's values, not only the injected ones.
	stdout := newRedactor(os.Stdout, envs)
	stderr := newRedactor(os.Stderr, envs)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = childEnv
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// When ta runs in a terminal, the child gets a pseudo-terminal of its own
	// in its place, so it behaves as when run there directly (colours,
	// progress bars, pagers, prompts); what it shows still goes through the
	// redactors. A stream redirected away from the terminal stays a pipe.
	// Without a terminal (agents, pipelines) the child gets pipes only.
	var term *feed
	var feeds []*feed
	closeFeeds := func() error {
		var errs []error
		for _, f := range feeds {
			errs = append(errs, f.wait())
		}
		return errors.Join(errs...)
	}
	outTerm := isTerminal(os.Stdin) && sameFile(os.Stdin, os.Stdout)
	errTerm := isTerminal(os.Stdin) && sameFile(os.Stdin, os.Stderr)
	if outTerm || errTerm {
		shows := stdout // the redactor that writes to ta's terminal
		if !outTerm {
			shows = stderr
		}
		// Ctrl-Z for the child's own group: stop it, so that waitStopping
		// hands the terminal back to the shell.
		suspend := func(foreground int) bool {
			return foreground == cmd.Process.Pid && syscall.Kill(-foreground, syscall.SIGSTOP) == nil
		}
		t, err := shows.terminal(os.Stdin, suspend)
		if err != nil {
			return 0, err
		}
		term = t
		feeds = append(feeds, t)
		cmd.Stdin = t.child
		cmd.Stdout, cmd.Stderr = t.child, t.child
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if !outTerm {
			p, err := stdout.pipe()
			if err != nil {
				closeFeeds()
				return 0, err
			}
			feeds = append(feeds, p)
			cmd.Stdout = p.child
		}
		if !errTerm {
			p, err := stderr.pipe()
			if err != nil {
				closeFeeds()
				return 0, err
			}
			feeds = append(feeds, p)
			cmd.Stderr = p.child
		}
	}
	if err := cmd.Start(); err != nil {
		closeFeeds()
		fmt.Fprintf(os.Stderr, "ta: %v\n", err)
		if errors.Is(err, fs.ErrPermission) {
			return 126, nil
		}
		return 127, nil
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for s := range sigs {
			cmd.Process.Signal(s)
		}
	}()
	var status syscall.WaitStatus
	var waitErr error
	if term != nil {
		term.resume()
		status, waitErr = waitStopping(cmd.Process, term)
	} else if waitErr = cmd.Wait(); cmd.ProcessState != nil {
		status = cmd.ProcessState.Sys().(syscall.WaitStatus)
	}
	signal.Stop(sigs)

	// The feeds must be through before the redactors flush what they hold
	// back; closing the terminal feed also gives ta's terminal back.
	if err := errors.Join(closeFeeds(), stdout.Flush(), stderr.Flush()); err != nil {
		return 0, err
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return 0, waitErr
	}
	if status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return status.ExitStatus(), nil
}

// validName accepts names usable as environment variables.
func validName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, c := range []byte(s) {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// validSlug accepts environment and project names such as dev, prod-eu or
// staging_2; they are also URL path segments and directory names.
func validSlug(s string) bool {
	if s == "" || s[0] == '_' || s[0] == '-' {
		return false
	}
	for _, c := range []byte(s) {
		if !(c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
