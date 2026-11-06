package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileGraph(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "---\nname: review-pr\nrequires: [ghost]\n---\n# Review\n\nRead [style](references/style.md).\nRun `scripts/collect-diff.sh` first.\nSee https://example.com/a/b.md and ~/notes/x.md.\nFill templates/report.md.\nUse the systematic-debugging skill.\n")
	write("references/style.md", "Style.\n")
	write("scripts/collect-diff.sh", "#!/bin/sh\nsource \"$(dirname \"$0\")/lib/git.sh\"\n")
	write("scripts/lib/git.sh", "git diff\n")
	write("unused.txt", "nothing\n")
	scope := []Entry{{Name: "review-pr"}, {Name: "systematic-debugging"}}
	g, err := FileGraph(Entry{Name: "review-pr", Path: dir}, scope)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]NodeKind{}
	for _, n := range g.Nodes {
		kinds[n.Name] = n.Kind
	}
	want := map[string]NodeKind{
		"SKILL.md": NodeFile, "references/style.md": NodeFile, "scripts/collect-diff.sh": NodeFile,
		"scripts/lib/git.sh": NodeFile, "templates/report.md": NodeMissingFile,
		"systematic-debugging": NodeSkill, "ghost": NodeMissing,
	}
	for name, kind := range want {
		if kinds[name] != kind {
			t.Fatalf("%s: got %q, want %q (all %v)", name, kinds[name], kind, kinds)
		}
	}
	if len(kinds) != len(want) {
		t.Fatalf("unexpected nodes %v", kinds)
	}
	edge := func(from, to string) Edge {
		for _, e := range g.Edges {
			if e.From == from && e.To == to {
				return e
			}
		}
		t.Fatalf("missing edge %s → %s in %+v", from, to, g.Edges)
		return Edge{}
	}
	if e := edge("SKILL.md", "references/style.md"); e.Line != 7 || e.Via != "链接" {
		t.Fatalf("link edge %+v", e)
	}
	if e := edge("scripts/collect-diff.sh", "scripts/lib/git.sh"); e.Via != "source" || e.Line != 2 {
		t.Fatalf("source edge %+v", e)
	}
	if e := edge("SKILL.md", "systematic-debugging"); !e.Inferred {
		t.Fatalf("mention should be inferred %+v", e)
	}
	if e := edge("SKILL.md", "templates/report.md"); e.Line != 10 {
		t.Fatalf("missing file edge %+v", e)
	}
	if len(g.Isolated) != 1 || g.Isolated[0] != "unused.txt" {
		t.Fatalf("isolated %v", g.Isolated)
	}
	if g.Nodes[0].Name != "SKILL.md" {
		t.Fatalf("SKILL.md should lead: %v", g.Nodes[0].Name)
	}
}
