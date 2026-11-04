package skill

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildEffectiveLayersAndShadowing(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "proj")
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	proj := filepath.Join(work, ".claude", "skills")

	writeSkill(t, filepath.Join(proj, "review"), "project review")
	writeSkill(t, filepath.Join(claude, "review"), "global review")
	writeSkill(t, filepath.Join(pool, "review"), "pool review")
	writeSkill(t, filepath.Join(claude, "tdd"), "global tdd")
	writeSkill(t, filepath.Join(pool, "writing"), "pool writing")
	if err := os.MkdirAll(filepath.Join(proj, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/skill-manager-nope", filepath.Join(claude, "gone")); err != nil {
		t.Fatal(err)
	}

	snap := Scan(Discover(home), nil)
	var agent Root
	for _, r := range snap.Roots {
		if r.Role == RoleAgent && r.Path == claude {
			agent = r
		}
	}
	v, err := BuildEffective(snap, home, agent, work)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]Effective{}
	for _, s := range v.Skills {
		got[s.Name] = s
	}
	if len(got) != 3 {
		t.Fatalf("skills: %v", v.Skills)
	}
	rev := got["review"]
	if rev.Winner().Layer != LayerProject || len(rev.Shadowed()) != 2 ||
		rev.Shadowed()[0].Layer != LayerGlobal || rev.Shadowed()[1].Layer != LayerPool {
		t.Fatalf("review: %+v", rev)
	}
	if got["tdd"].Winner().Layer != LayerGlobal || got["writing"].Winner().Layer != LayerPool {
		t.Fatalf("winners: %+v %+v", got["tdd"], got["writing"])
	}
	if _, ok := got["empty"]; ok {
		t.Fatal("an empty directory was loaded")
	}
	if _, ok := got["gone"]; ok {
		t.Fatal("a broken link was loaded")
	}
	if v.Overridden() != 1 {
		t.Fatalf("overridden: %d", v.Overridden())
	}
	counts := map[Layer]int{}
	for _, l := range v.Layers {
		counts[l.Layer] = l.Count
	}
	if counts[LayerProject] != 1 || counts[LayerGlobal] != 2 || counts[LayerPool] != 2 || counts[LayerBuiltin] != 0 {
		t.Fatalf("layer counts: %v", counts)
	}

	// Another directory changes the answer: with no project folder the
	// global copy wins.
	other, err := BuildEffective(snap, home, agent, filepath.Join(home, "elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range other.Skills {
		if s.Name == "review" && s.Winner().Layer != LayerGlobal {
			t.Fatalf("elsewhere: %+v", s)
		}
	}
}

func TestProjectRoot(t *testing.T) {
	home := "/h"
	claude := Root{ID: "claude", Path: "/h/.claude/skills", Role: RoleAgent}
	if r, ok := ProjectRoot(home, claude, "/h/code/p"); !ok || r.Path != "/h/code/p/.claude/skills" {
		t.Fatalf("claude: %v %v", r, ok)
	}
	if _, ok := ProjectRoot(home, claude, "/h"); ok {
		t.Fatal("home is not a project")
	}
	if _, ok := ProjectRoot(home, claude, ""); ok {
		t.Fatal("no directory chosen")
	}
	nested := Root{ID: "opencode", Path: "/h/.config/opencode/skills", Role: RoleAgent}
	if _, ok := ProjectRoot(home, nested, "/h/code/p"); ok {
		t.Fatal("nested agent folder has no known project folder")
	}
}

func TestAgeNote(t *testing.T) {
	now := time.Now()
	w := Entry{DocModTime: now, RealPath: "/a"}
	old := Entry{DocModTime: now.Add(-49 * time.Hour), RealPath: "/b"}
	if got := AgeNote(w, old); got != "比胜出的版本旧 2 天" {
		t.Fatalf("old: %q", got)
	}
	if got := AgeNote(w, Entry{RealPath: "/a", DocModTime: now}); got != "指向同一个目录" {
		t.Fatalf("same: %q", got)
	}
}
