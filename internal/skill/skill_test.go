package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFront(t *testing.T) {
	name, desc := ParseFront("---\nname: lark-im\ndescription: \"发消息\"\n---\n\n# im\n")
	if name != "lark-im" || desc != "发消息" {
		t.Fatalf("quoted front matter: %q %q", name, desc)
	}
	_, desc = ParseFront("---\ndescription: >\n  第一行\n  第二行\n---\n")
	if desc != "第一行 第二行" {
		t.Fatalf("folded description: %q", desc)
	}
	_, desc = ParseFront("# Title\n\n正文第一段\n")
	if desc != "正文第一段" {
		t.Fatalf("body: %q", desc)
	}
}

func TestTreeHashIgnoresDSStore(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	body := []byte("---\nname: a\ndescription: \"same\"\n---\n")
	if err := os.WriteFile(filepath.Join(a, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	ha, err := TreeHash(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := TreeHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("DS_Store changed the hash: %s vs %s", ha, hb)
	}
	if err := os.WriteFile(filepath.Join(b, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	hb, err = TreeHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha == hb {
		t.Fatal("extra file did not change the hash")
	}
}

func TestScanRelationsAndRepos(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	builtin := filepath.Join(home, ".cursor", "skills-cursor")
	writeSkill(t, filepath.Join(pool, "alpha"), "alpha")
	writeSkill(t, filepath.Join(pool, "beta"), "beta")
	writeSkill(t, filepath.Join(pool, "cloud"), "cloud v2")
	writeSkill(t, filepath.Join(pool, "nested"), "parent")
	writeSkill(t, filepath.Join(pool, "nested", "skills", "child"), "child")
	writeSkill(t, filepath.Join(pool, "observe"), "good observe")
	if err := os.MkdirAll(filepath.Join(pool, "blank"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/missing-baocut-test", filepath.Join(pool, "gone")); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRel(t, claude, "alpha", filepath.Join(pool, "alpha"))
	copySkill(t, filepath.Join(pool, "beta"), filepath.Join(claude, "beta"))
	writeSkill(t, filepath.Join(claude, "cloud"), "cloud v1")
	if err := os.Symlink("/tmp/missing-baocut-test", filepath.Join(claude, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/missing-observe-test", filepath.Join(claude, "observe")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(claude, "extra"), "only claude")
	copySkill(t, filepath.Join(pool, "alpha"), filepath.Join(builtin, "alpha"))

	decoy := filepath.Join(home, ".grok", "marketplace-cache", "abc", "skills", "nope")
	writeSkill(t, decoy, "cached")
	writeSkill(t, filepath.Join(home, ".claude", "plugins", "marketplaces", "ecc", "skills", "deep-research"), "cached")
	writeSkill(t, filepath.Join(home, ".codex", "tmp", "plugins", "agents", "skills", "paper"), "cached")
	writeSkill(t, filepath.Join(home, ".codex", ".tmp", "plugins", ".agents", "skills", "paper"), "cached")
	writeSkill(t, filepath.Join(home, ".vscode", "extensions", "some.tool", "skills", "tool"), "cached")

	writeSkill(t, filepath.Join(home, ".unknown", "skills", "nope"), "unknown")
	writeSkill(t, filepath.Join(home, ".claude", "other", "skills", "nope"), "nested")
	repo := t.TempDir()
	writeSkill(t, filepath.Join(repo, "gamma"), "gamma")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha but edited")
	writeSkill(t, filepath.Join(repo, "pkg", "tool"), "a package skill")

	roots := Discover(home)
	if !hasRoot(roots, pool, RolePool) {
		t.Fatalf("pool missing: %+v", roots)
	}
	if !hasRoot(roots, claude, RoleAgent) {
		t.Fatalf("claude missing: %+v", roots)
	}
	if !hasRoot(roots, builtin, RoleBuiltin) {
		t.Fatalf("builtin missing: %+v", roots)
	}
	for _, r := range roots {
		if filepath.Base(filepath.Dir(r.Path)) == "abc" || strings.Contains(r.Path, "marketplaces") || strings.Contains(r.Path, "extensions") || strings.Contains(r.Path, "/tmp/") || strings.Contains(r.Path, "/.tmp/") || strings.Contains(r.Path, "/.unknown/") || strings.Contains(r.Path, "/other/") {
			t.Fatalf("install cache was discovered: %s", r.Path)
		}
	}

	snap := Scan(roots, []string{repo, filepath.Join(repo, "missing")})
	if len(snap.Warnings) != 1 || !strings.Contains(snap.Warnings[0], "仓库不存在") {
		t.Fatalf("only missing repo should warn, not ordinary broken links: %v", snap.Warnings)
	}

	alpha := mustEntry(t, snap, "claude-skills", "alpha")
	if alpha.Relation != RelLinked {
		t.Fatalf("alpha relation %s, link %s", alpha.Relation, alpha.LinkAbs)
	}
	beta := mustEntry(t, snap, "claude-skills", "beta")
	if beta.Relation != RelCopy {
		t.Fatalf("beta relation %s", beta.Relation)
	}
	cloud := mustEntry(t, snap, "claude-skills", "cloud")
	if cloud.Relation != RelCopy {
		t.Fatalf("cloud relation %s", cloud.Relation)
	}
	if mustEntry(t, snap, "claude-skills", "extra").Relation != RelLocal {
		t.Fatal("extra should be local")
	}
	if mustEntry(t, snap, "claude-skills", "observe").Relation != RelBroken {
		t.Fatal("observe should be broken")
	}
	nested := mustEntry(t, snap, "pool", "nested")
	if len(nested.Children) != 0 {
		t.Fatalf("nested children were scanned: %+v", nested.Children)
	}
	builtinAlpha := mustEntry(t, snap, "cursor-skills-cursor", "alpha")
	if builtinAlpha.Relation != RelSystem {
		t.Fatalf("builtin relation %s", builtinAlpha.Relation)
	}

	want := map[IssueKind]map[string]bool{
		IssueBroken:    {},
		IssueStale:     {},
		IssueDuplicate: {},
		IssueEmpty:     {},
		IssueOrphan:    {},
	}
	for _, issue := range snap.Issues {
		if want[issue.Kind] == nil {
			t.Fatalf("unexpected issue %+v", issue)
		}
		want[issue.Kind][issue.Entry.RootID+"/"+issue.Entry.Name] = true
	}
	requireIssue(t, want[IssueBroken], "pool/gone", "claude-skills/gone", "claude-skills/observe")
	requireIssue(t, want[IssueEmpty], "pool/blank")
	requireIssue(t, want[IssueOrphan], "pool/nested")
	for kind, got := range want {
		if len(got) != 0 {
			t.Fatalf("unasserted %s issues: %v", kind, got)
		}
	}

	repoID := "repo:" + filepath.Clean(repo)
	if mustEntry(t, snap, repoID, "gamma").Relation != RelLocal {
		t.Fatal("gamma should be local to the repo")
	}
	if mustEntry(t, snap, repoID, "alpha").Relation != RelCopy {
		t.Fatal("repo alpha should await comparison")
	}
	if _, ok := findEntry(snap, repoID, "tool"); ok {
		t.Fatal("nested package skill was scanned")
	}
	if _, ok := findEntry(snap, repoID, "child"); ok {
		t.Fatal("child skill was listed separately from its parent")
	}
}

func TestParseRefs(t *testing.T) {
	md := "---\nname: ship\nrequires:\n  - implement\n  - \"review\"\n---\n" +
		"Run `design` first, then `lint`. See [x](../docs/SKILL.md) and Skill(deploy).\n" +
		"Also use the pdf skill, or /compress. Not `a file.txt` or `src/x.go`.\n"
	requires, mentions := ParseRefs(md)
	if strings.Join(requires, ",") != "implement,review" {
		t.Fatalf("requires %v", requires)
	}
	got := map[string]bool{}
	for _, m := range mentions {
		got[m] = true
	}
	for _, want := range []string{"design", "lint", "docs", "deploy", "pdf", "compress"} {
		if !got[want] {
			t.Errorf("mention %q missing in %v", want, mentions)
		}
	}
	if got["x.go"] || got["file.txt"] || got["src/x.go"] {
		t.Errorf("file names counted as skills: %v", mentions)
	}
	inline, _ := ParseRefs("---\ndepends_on: [a, \"b\"]\n---\n")
	if strings.Join(inline, ",") != "a,b" {
		t.Fatalf("inline list %v", inline)
	}
}

func TestBuildGraphLayersCyclesAndMissing(t *testing.T) {
	entries := []Entry{
		{Name: "ship", Requires: []string{"build", "ghost"}},
		{Name: "build", Mentions: []string{"lint", "Ship"}},
		{Name: "lint"},
		{Name: "solo", Children: nil},
		{Name: "suite", Children: []string{"part"}},
	}
	g := BuildGraph(entries)
	if len(g.Isolated) != 1 || g.Isolated[0] != "solo" {
		t.Fatalf("isolated %v", g.Isolated)
	}
	node := func(name string) Node {
		n, ok := g.Node(name)
		if !ok {
			t.Fatalf("no node %s in %+v", name, g.Nodes)
		}
		return n
	}
	if node("ghost").Kind != NodeMissing || node("part").Kind != NodeChild {
		t.Fatalf("kinds: ghost=%s part=%s", node("ghost").Kind, node("part").Kind)
	}
	for _, e := range g.Edges {
		if !e.Cycle && node(e.From).Layer >= node(e.To).Layer {
			t.Fatalf("edge %s -> %s does not run left to right", e.From, e.To)
		}
	}
	cycles := 0
	for _, e := range g.Edges {
		if e.Cycle {
			cycles++
		}
	}
	if cycles != 1 {
		t.Fatalf("want exactly one edge closing the ship/build loop, got %d: %+v", cycles, g.Edges)
	}
	if got := g.NeededBy("lint"); len(got) != 1 || got[0] != "build" {
		t.Fatalf("needed by lint: %v", got)
	}
	if got := g.Needs("suite"); len(got) != 0 {
		t.Fatalf("containing a child is not a dependency: %v", got)
	}
}

func TestLiveHome(t *testing.T) {
	if os.Getenv("SKILL_MANAGER_LIVE") == "" {
		t.Skip("set SKILL_MANAGER_LIVE=1 to scan the real home directory")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	roots := Discover(home)
	for _, root := range roots {
		s := filepath.ToSlash(root.Path)
		for _, p := range []string{"/.tmp/", "/tmp/plugins/", "/plugins/marketplaces/", "/plugins/managed/", "/.vscode/extensions/", "/.vscode-server/extensions/", "/raycast/extensions/"} {
			if strings.Contains(s, p) {
				t.Errorf("install cache discovered: %s", root.Path)
			}
		}
	}
	snap := Scan(roots, nil)
	t.Logf("roots=%d entries=%d issues=%d warnings=%d", len(snap.Roots), len(snap.Entries), len(snap.Issues), len(snap.Warnings))
	for _, sum := range snap.Summaries {
		t.Logf("%s total=%d linked=%d same=%d stale=%d broken=%d empty=%d local=%d external=%d system=%d issues=%d",
			sum.Root.Name, sum.Total, sum.Linked, sum.Same, sum.Stale, sum.Broken, sum.Empty, sum.Local, sum.External, sum.System, sum.Issues)
	}
	counts := map[IssueKind]int{}
	for _, issue := range snap.Issues {
		counts[issue.Kind]++
	}
	t.Logf("broken=%d stale=%d duplicate=%d empty=%d orphan=%d",
		counts[IssueBroken], counts[IssueStale], counts[IssueDuplicate], counts[IssueEmpty], counts[IssueOrphan])
	dups := map[string]int{}
	for _, issue := range snap.Issues {
		switch issue.Kind {
		case IssueBroken, IssueEmpty, IssueOrphan, IssueStale:
			t.Logf("issue %s %s/%s", issue.Kind, issue.Entry.RootName, issue.Entry.Name)
		case IssueDuplicate:
			dups[issue.Entry.Name]++
		}
	}
	for name, n := range dups {
		t.Logf("duplicate %s x%d", name, n)
	}
	for _, name := range []string{"baocut", "superpowers"} {
		e, ok := findEntry(snap, "pool", name)
		if !ok || e.Relation != RelBroken {
			t.Errorf("pool %s: broken=%v ok=%v", name, e.Relation, ok)
		}
	}
	if _, ok := findEntry(snap, "pool", "anysearch"); !ok {
		t.Error("pool is missing anysearch")
	}
	for _, e := range snap.Entries {
		if e.SkillHash != "" || e.TreeHash != "" || len(e.Mentions) != 0 || len(e.Children) != 0 {
			t.Errorf("automatic content scan for %s", e.Path)
		}
	}

}

func writeSkill(t *testing.T, dir, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(dir) + "\ndescription: \"" + desc + "\"\n---\n\n# " + filepath.Base(dir) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copySkill(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func linkRel(t *testing.T, dir, name, target string) {
	t.Helper()
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func hasRoot(roots []Root, path string, role Role) bool {
	path = filepath.Clean(path)
	for _, r := range roots {
		if r.Path == path && r.Role == role {
			return true
		}
	}
	return false
}

func mustEntry(t *testing.T, snap *Snapshot, rootID, name string) Entry {
	t.Helper()
	e, ok := findEntry(snap, rootID, name)
	if !ok {
		t.Fatalf("missing %s/%s", rootID, name)
	}
	return e
}

func findEntry(snap *Snapshot, rootID, name string) (Entry, bool) {
	for _, e := range snap.Entries {
		if e.RootID == rootID && e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

func requireIssue(t *testing.T, got map[string]bool, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if !got[key] {
			t.Fatalf("missing issue %s in %v", key, got)
		}
		delete(got, key)
	}
}
