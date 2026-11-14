package main

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"text/tabwriter"
)

// The agent skill teaches coding agents to use secrets through ta. Like any
// skill it is a SKILL.md in a directory named after it, but `ta skill setup`
// installs only a stub, which asks the agent to run `ta skill show`; that
// prints the guide built into this binary, so upgrading ta also updates what
// agents are told. skills/tantalum/SKILL.md in the repository is the same stub.

//go:embed skill/guide.md
var skillGuide []byte

//go:embed skill/stub.md
var skillStub []byte

const (
	skillName = "tantalum"
	skillFile = "SKILL.md"
	// sharedSkills is the skills directory most agents read, relative to the
	// home directory; setup uses it when no agent is found.
	sharedSkills = ".agents/skills"
)

// skillAgent is an agent ta can give the skill to. Paths are relative to the
// home directory.
type skillAgent struct {
	id, name string
	config   string // present when the agent is installed
	skills   string // where the agent reads skills from
}

var skillAgents = []skillAgent{
	{"claude", "Claude Code", ".claude", ".claude/skills"},
	{"codex", "Codex", ".codex", sharedSkills},
	{"opencode", "OpenCode", ".config/opencode", sharedSkills},
	{"pi", "Pi", ".pi", sharedSkills},
	{"omp", "omp", ".omp", sharedSkills},
}

func (a skillAgent) found(home string) bool {
	_, err := os.Stat(filepath.Join(home, a.config))
	return err == nil
}

// skillState is what a skill directory holds.
type skillState string

const (
	skillMissing    skillState = "missing"    // nothing there
	skillCurrent    skillState = "current"    // this ta's stub
	skillOutdated   skillState = "outdated"   // tantalum's skill from an older ta, or edited since
	skillForeign    skillState = "foreign"    // something else under the name tantalum
	skillUnreadable skillState = "unreadable" // could not be read
)

func skillStateOf(dir string) skillState {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return skillMissing
	} else if err != nil {
		return skillUnreadable
	}
	data, err := os.ReadFile(filepath.Join(dir, skillFile))
	switch {
	case err == nil && bytes.Equal(data, skillStub):
		return skillCurrent
	case err == nil && skillNamed(data) == skillName:
		return skillOutdated
	case err == nil, errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return skillForeign
	default:
		return skillUnreadable
	}
}

// skillNamed returns the top-level name in a SKILL.md's front matter.
func skillNamed(data []byte) string {
	lines := strings.Split(string(data), "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, " \t\r")
		if line == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// skillTarget is one skill directory and the agents that read it; agents that
// share a skills directory share a target, so it is written once.
type skillTarget struct {
	dir    string
	agents []string
}

func skillTargets(home string, agents []skillAgent) []skillTarget {
	var targets []skillTarget
	for _, a := range agents {
		dir := filepath.Join(home, a.skills, skillName)
		i := slices.IndexFunc(targets, func(t skillTarget) bool { return t.dir == dir })
		if i < 0 {
			targets = append(targets, skillTarget{dir: dir})
			i = len(targets) - 1
		}
		targets[i].agents = append(targets[i].agents, a.name)
	}
	return targets
}

// pickSkillAgents returns the agents named by ids, or with no ids every agent
// (found only, when foundOnly is set).
func pickSkillAgents(home string, ids []string, foundOnly bool) ([]skillAgent, error) {
	var picked []skillAgent
	for _, id := range ids {
		i := slices.IndexFunc(skillAgents, func(a skillAgent) bool { return a.id == id })
		if i < 0 {
			return nil, fmt.Errorf("unknown agent %q: use %s", id, skillAgentIDs())
		}
		if !slices.Contains(picked, skillAgents[i]) {
			picked = append(picked, skillAgents[i])
		}
	}
	if len(ids) > 0 {
		return picked, nil
	}
	for _, a := range skillAgents {
		if !foundOnly || a.found(home) {
			picked = append(picked, a)
		}
	}
	return picked, nil
}

func skillAgentIDs() string {
	ids := make([]string, len(skillAgents))
	for i, a := range skillAgents {
		ids[i] = a.id
	}
	return strings.Join(ids, ", ")
}

// tildePath shows a path under home as ~/...
func tildePath(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

func cmdSkill(args []string) error {
	const usage = "usage: ta skill setup [-agent AGENT]... [-dry-run] [-force] | status | remove [-agent AGENT]... [-dry-run] | show"
	if len(args) == 0 {
		return errors.New(usage)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "show":
		if len(args) != 0 {
			return errors.New("usage: ta skill show")
		}
		_, err := os.Stdout.Write(skillGuide)
		return err
	case "status":
		if len(args) != 0 {
			return errors.New("usage: ta skill status")
		}
		return skillStatus(home, os.Stdout)
	case "setup", "remove":
		flags := flag.NewFlagSet(sub, flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		var ids specList
		flags.Var(&ids, "agent", "")
		dryRun := flags.Bool("dry-run", false, "")
		force := new(bool)
		if sub == "setup" {
			flags.BoolVar(force, "force", false, "")
		}
		if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
			if sub == "setup" {
				return errors.New("usage: ta skill setup [-agent AGENT]... [-dry-run] [-force]")
			}
			return errors.New("usage: ta skill remove [-agent AGENT]... [-dry-run]")
		}
		if sub == "setup" {
			return skillSetup(home, ids, *dryRun, *force, os.Stderr)
		}
		return skillRemove(home, ids, *dryRun, os.Stderr)
	default:
		return errors.New(usage)
	}
}

// skillSetup installs the stub for the agents named by ids, or else for the
// agents found in home, or else in the shared skills directory. A skill named
// tantalum is updated; anything else under that name is left alone unless
// force is set.
func skillSetup(home string, ids []string, dryRun, force bool, w io.Writer) error {
	agents, err := pickSkillAgents(home, ids, true)
	if err != nil {
		return err
	}
	targets := skillTargets(home, agents)
	if len(targets) == 0 {
		targets = []skillTarget{{
			dir:    filepath.Join(home, sharedSkills, skillName),
			agents: []string{"no agent found; the directory most agents read"},
		}}
	}
	conflicts := 0
	for _, t := range targets {
		var done, todo string
		switch state := skillStateOf(t.dir); {
		case state == skillCurrent:
			fmt.Fprintf(w, "%-13s %s (%s)\n", "up to date", tildePath(home, t.dir), strings.Join(t.agents, ", "))
			continue
		case state == skillMissing:
			done, todo = "installed", "would install"
		case state == skillOutdated:
			done, todo = "updated", "would update"
		case state == skillForeign && force:
			done, todo = "replaced", "would replace"
		default:
			conflicts++
			fmt.Fprintf(w, "%-13s %s: %s\n", "left alone", tildePath(home, t.dir), skillLeftAlone(state, true))
			continue
		}
		if dryRun {
			done = todo
		} else if err := installSkill(t.dir); err != nil {
			return err
		}
		fmt.Fprintf(w, "%-13s %s (%s)\n", done, tildePath(home, t.dir), strings.Join(t.agents, ", "))
	}
	if conflicts > 0 {
		return fmt.Errorf("%d skill directories left alone", conflicts)
	}
	return nil
}

// skillLeftAlone says why a skill directory was not changed.
func skillLeftAlone(state skillState, forceHelps bool) string {
	if state == skillUnreadable {
		return "could not be read"
	}
	if forceHelps {
		return "not tantalum's skill; -force replaces it"
	}
	return "not tantalum's skill"
}

// installSkill writes the stub into dir. A symlink or file at dir is replaced
// by a directory rather than written through, so what it points to is kept.
func installSkill(dir string) error {
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		if err := os.Remove(dir); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(dir, skillFile, skillStub); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(dir, skillFile), 0o644)
}

// skillRemove removes tantalum's skill from the agents named by ids, or from
// every agent ta knows, found or not, so nothing is left behind. Anything else
// under the name tantalum is left alone.
func skillRemove(home string, ids []string, dryRun bool, w io.Writer) error {
	agents, err := pickSkillAgents(home, ids, false)
	if err != nil {
		return err
	}
	for _, t := range skillTargets(home, agents) {
		state := skillStateOf(t.dir)
		if state == skillMissing {
			continue
		}
		if state != skillCurrent && state != skillOutdated {
			fmt.Fprintf(w, "%-13s %s: %s\n", "left alone", tildePath(home, t.dir), skillLeftAlone(state, false))
			continue
		}
		done := "removed"
		if dryRun {
			done = "would remove"
		} else if err := uninstallSkill(t.dir); err != nil {
			return err
		}
		fmt.Fprintf(w, "%-13s %s (%s)\n", done, tildePath(home, t.dir), strings.Join(t.agents, ", "))
	}
	return nil
}

// uninstallSkill removes the skill at dir: a symlink itself, not what it
// points to; or SKILL.md, then the directory when nothing else is in it.
func uninstallSkill(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return os.Remove(dir)
	}
	if err := os.Remove(filepath.Join(dir, skillFile)); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, syscall.ENOTEMPTY) {
		return err
	}
	return nil
}

// skillStatus reports, for every agent ta knows, whether it was found and
// what its skill directory holds. Nothing is changed.
func skillStatus(home string, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tFOUND\tSKILL\tPATH")
	for _, a := range skillAgents {
		found := "no"
		if a.found(home) {
			found = "yes"
		}
		dir := filepath.Join(home, a.skills, skillName)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.id, found, skillStateOf(dir), tildePath(home, dir))
	}
	return tw.Flush()
}
