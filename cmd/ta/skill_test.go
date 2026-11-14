package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillHome is a home directory holding the config directories of agents.
func skillHome(t *testing.T, configs ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, c := range configs {
		if err := os.MkdirAll(filepath.Join(home, c), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func writeSkill(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSkill(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, skillFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const oldFullSkill = "---\nname: tantalum\ndescription: old\n---\n\n# tantalum\n"

func TestSkillStubIsTheRepositorySkill(t *testing.T) {
	repo, err := os.ReadFile(filepath.Join("..", "..", "skills", skillName, skillFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo, skillStub) {
		t.Error("skills/tantalum/SKILL.md differs from cmd/ta/skill/stub.md; keep them the same")
	}
	if skillNamed(skillStub) != skillName {
		t.Errorf("the stub's front matter names %q, want %q", skillNamed(skillStub), skillName)
	}
	if !bytes.Contains(skillStub, []byte("`ta skill show`")) || !bytes.HasPrefix(skillGuide, []byte("# tantalum\n")) {
		t.Error("the stub should send agents to `ta skill show`, which prints the guide")
	}
}

func TestSkillSetupInstallsIntoFoundAgentsOnce(t *testing.T) {
	home := skillHome(t, ".claude", ".codex", ".pi")
	var out bytes.Buffer
	if err := skillSetup(home, nil, false, false, &out); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".claude/skills/tantalum", ".agents/skills/tantalum"} {
		if got := readSkill(t, filepath.Join(home, dir)); got != string(skillStub) {
			t.Errorf("%s: %q, want the stub", dir, got)
		}
		fi, err := os.Stat(filepath.Join(home, dir, skillFile))
		if err != nil || fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: mode %v (%v), want 0644", dir, fi.Mode(), err)
		}
	}
	if !strings.Contains(out.String(), "~/.agents/skills/tantalum (Codex, Pi)") || strings.Count(out.String(), "installed") != 2 {
		t.Errorf("setup output:\n%s", out.String())
	}

	out.Reset()
	if err := skillSetup(home, nil, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "up to date") != 2 {
		t.Errorf("second setup:\n%s", out.String())
	}
}

func TestSkillSetupWithNoAgentUsesTheSharedDirectory(t *testing.T) {
	home := skillHome(t)
	if err := skillSetup(home, nil, false, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := readSkill(t, filepath.Join(home, sharedSkills, skillName)); got != string(skillStub) {
		t.Errorf("shared skill: %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		t.Error("setup created ~/.claude without Claude Code")
	}
}

func TestSkillSetupNamedAgentOnly(t *testing.T) {
	home := skillHome(t, ".claude", ".codex")
	if err := skillSetup(home, []string{"claude"}, false, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if skillStateOf(filepath.Join(home, ".claude/skills/tantalum")) != skillCurrent {
		t.Error("claude skill not installed")
	}
	if skillStateOf(filepath.Join(home, ".agents/skills/tantalum")) != skillMissing {
		t.Error("setup -agent claude also installed the shared skill")
	}
	if err := skillSetup(home, []string{"nope"}, false, false, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("unknown agent: %v", err)
	}
}

func TestSkillSetupReplacesAnOldSkillAndKeepsWhatALinkPointsTo(t *testing.T) {
	home := skillHome(t, ".claude", ".codex")
	clone := filepath.Join(t.TempDir(), "skills", "tantalum")
	writeSkill(t, clone, oldFullSkill)
	link := filepath.Join(home, ".claude/skills/tantalum")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(clone, link); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(home, ".agents/skills/tantalum"), oldFullSkill)
	if s := skillStateOf(link); s != skillOutdated {
		t.Fatalf("linked old skill is %s, want outdated", s)
	}

	var out bytes.Buffer
	if err := skillSetup(home, nil, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "updated") != 2 {
		t.Errorf("setup output:\n%s", out.String())
	}
	if fi, err := os.Lstat(link); err != nil || !fi.IsDir() {
		t.Errorf("the link was not replaced by a directory: %v %v", fi, err)
	}
	if got := readSkill(t, clone); got != oldFullSkill {
		t.Errorf("setup wrote through the link into the clone: %q", got)
	}
	for _, dir := range []string{link, filepath.Join(home, ".agents/skills/tantalum")} {
		if s := skillStateOf(dir); s != skillCurrent {
			t.Errorf("%s is %s after setup", dir, s)
		}
	}
}

func TestSkillSetupLeavesAForeignSkillUnlessForced(t *testing.T) {
	home := skillHome(t, ".codex")
	dir := filepath.Join(home, ".agents/skills/tantalum")
	foreign := "---\nname: something-else\n---\n"
	writeSkill(t, dir, foreign)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := skillSetup(home, nil, false, false, &out); err == nil {
		t.Error("setup over a foreign skill succeeded; want an error")
	}
	if got := readSkill(t, dir); got != foreign || !strings.Contains(out.String(), "-force replaces it") {
		t.Errorf("foreign skill changed to %q; output:\n%s", got, out.String())
	}

	if err := skillSetup(home, nil, false, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if skillStateOf(dir) != skillCurrent {
		t.Error("-force did not install the stub")
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("-force removed another file in the directory: %v", err)
	}
}

func TestSkillDryRunChangesNothing(t *testing.T) {
	home := skillHome(t, ".claude")
	writeSkill(t, filepath.Join(home, ".claude/skills/tantalum"), oldFullSkill)
	var out bytes.Buffer
	if err := skillSetup(home, nil, true, false, &out); err != nil {
		t.Fatal(err)
	}
	if got := readSkill(t, filepath.Join(home, ".claude/skills/tantalum")); got != oldFullSkill || !strings.Contains(out.String(), "would update") {
		t.Errorf("dry-run setup: skill %q, output:\n%s", got, out.String())
	}
	out.Reset()
	if err := skillRemove(home, nil, true, &out); err != nil {
		t.Fatal(err)
	}
	if skillStateOf(filepath.Join(home, ".claude/skills/tantalum")) != skillOutdated || !strings.Contains(out.String(), "would remove") {
		t.Errorf("dry-run remove changed the skill; output:\n%s", out.String())
	}
}

func TestSkillRemoveTakesOnlyTantalumsSkill(t *testing.T) {
	home := skillHome(t, ".claude")
	if err := skillSetup(home, nil, false, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// Codex is not installed, but an old copy in its directory still goes.
	shared := filepath.Join(home, ".agents/skills/tantalum")
	writeSkill(t, shared, oldFullSkill)
	if err := os.WriteFile(filepath.Join(shared, "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, ".claude/skills/other")
	writeSkill(t, foreign, "---\nname: other\n---\n")

	if err := skillRemove(home, nil, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills/tantalum")); !os.IsNotExist(err) {
		t.Errorf("Claude Code's skill still exists (%v)", err)
	}
	// SKILL.md goes; the directory stays for the file ta did not write.
	if _, err := os.Lstat(filepath.Join(shared, skillFile)); !os.IsNotExist(err) {
		t.Errorf("shared SKILL.md still exists (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "notes.txt")); err != nil {
		t.Errorf("remove took another file: %v", err)
	}
	if readSkill(t, foreign) == "" {
		t.Error("remove touched another skill")
	}
}

func TestSkillRemoveUnlinksWithoutTouchingTheTarget(t *testing.T) {
	home := skillHome(t, ".claude")
	clone := filepath.Join(t.TempDir(), "tantalum")
	writeSkill(t, clone, string(skillStub))
	link := filepath.Join(home, ".claude/skills/tantalum")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(clone, link); err != nil {
		t.Fatal(err)
	}
	if err := skillRemove(home, []string{"claude"}, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("link still exists (%v)", err)
	}
	if readSkill(t, clone) != string(skillStub) {
		t.Error("remove changed the link's target")
	}
}

func TestSkillRemoveKeepsAForeignSkill(t *testing.T) {
	home := skillHome(t, ".claude")
	dir := filepath.Join(home, ".claude/skills/tantalum")
	writeSkill(t, dir, "no front matter\n")
	var out bytes.Buffer
	if err := skillRemove(home, nil, false, &out); err != nil {
		t.Fatal(err)
	}
	if readSkill(t, dir) != "no front matter\n" || !strings.Contains(out.String(), "left alone") {
		t.Errorf("foreign skill removed; output:\n%s", out.String())
	}
}

func TestSkillStatusReportsEveryAgent(t *testing.T) {
	home := skillHome(t, ".claude", ".config/opencode")
	if err := skillSetup(home, []string{"claude"}, false, false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := skillStatus(home, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1+len(skillAgents) {
		t.Fatalf("status:\n%s", out.String())
	}
	want := map[string][]string{
		"claude":   {"yes", "current", "~/.claude/skills/tantalum"},
		"opencode": {"yes", "missing", "~/.agents/skills/tantalum"},
		"pi":       {"no", "missing", "~/.agents/skills/tantalum"},
	}
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if w, ok := want[f[0]]; ok && strings.Join(f[1:], " ") != strings.Join(w, " ") {
			t.Errorf("%s: %q, want %q", f[0], f[1:], w)
		}
	}
}
