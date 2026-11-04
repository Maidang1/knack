package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanMetadata(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	agent := filepath.Join(home, ".claude", "skills")
	dir := filepath.Join(pool, "large")
	writeSkill(t, dir, "placeholder")
	md := "---\nname: declared\ndescription: known description\nrequires: [support]\n---\n"
	f, err := os.Create(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(md + "use the body-only skill; `body-only`\n"); err != nil {
		t.Fatal(err)
	}
	// A sparse body would require 512 MiB to read in full.
	if err := f.Truncate(512 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRel(t, agent, "large", dir)
	writeSkill(t, filepath.Join(pool, "large", "skills", "nested"), "child")
	writeSkill(t, filepath.Join(agent, ".system", "builtin"), "shipped")
	writeSkill(t, filepath.Join(agent, ".system", "builtin", "skills", "deep"), "deep")
	writeSkill(t, filepath.Join(pool, "no-front"), "placeholder")
	if err := os.WriteFile(filepath.Join(pool, "no-front", "SKILL.md"), []byte("# Title\n\nDo not use body as description."), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := Scan(Discover(home), nil)
	e := mustEntry(t, snap, "pool", "large")
	if e.FrontName != "declared" || e.Description != "known description" || strings.Join(e.Requires, ",") != "support" {
		t.Fatalf("metadata: %+v", e)
	}
	if !e.HasDocument || !e.Usable() || e.DocSize != 512*1024*1024 || e.DocModTime.IsZero() || e.EntryModTime.IsZero() {
		t.Fatalf("document stamp: %+v", e)
	}
	linked := mustEntry(t, snap, "claude-skills", "large")
	if !linked.Usable() || linked.Relation != RelLinked || linked.DocSize != e.DocSize || linked.RealPath != e.RealPath {
		t.Fatalf("linked metadata: %+v", linked)
	}
	for _, entry := range snap.Entries {
		if entry.SkillHash != "" || entry.TreeHash != "" || len(entry.Mentions) != 0 || len(entry.Children) != 0 {
			t.Fatalf("body, children or attachments scanned: %+v", entry)
		}
	}
	if _, ok := findEntry(snap, "pool", "nested"); ok {
		t.Fatal("nested skill enumerated")
	}
	system := mustEntry(t, snap, "claude-skills", "builtin")
	if !system.System || system.Relation != RelSystem {
		t.Fatalf("system: %+v", system)
	}
	if _, ok := findEntry(snap, "claude-skills", "deep"); ok {
		t.Fatal("system child recursively enumerated")
	}
	plain := mustEntry(t, snap, "pool", "no-front")
	if plain.Description != "" || plain.FrontName != "" || !plain.Usable() {
		t.Fatalf("no front matter: %+v", plain)
	}
}

func TestReadHeader(t *testing.T) {
	cases := []struct {
		name, body string
		wantErr    bool
	}{
		{"plain", "# Body\n", false},
		{"empty", "", false},
		{"crlf", "\uFEFF---\r\nname: good\r\n---\r\nbody", false},
		{"no description", "---\nname: good\n---\nbody description", false},
		{"unclosed", "---\nname: bad\n", true},
		{"exact limit", "---\n" + strings.Repeat("#", maxHeaderSize-9) + "\n---\n", false},
		{"over limit", "---\n" + strings.Repeat("#", maxHeaderSize-8) + "\n---\n", true},
		{"long line", "---\n" + strings.Repeat("x", maxHeaderSize*2), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".agents", "skills", "entry")
			writeSkill(t, dir, "placeholder")
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			snap := Scan(Discover(home), nil)
			e := mustEntry(t, snap, "pool", "entry")
			if (e.MetaErr != "") != tc.wantErr || e.Usable() == tc.wantErr || !e.HasDocument || e.HashErr != "" {
				t.Fatalf("header readability: %+v", e)
			}
			if (len(snap.Warnings) != 0) != tc.wantErr {
				t.Fatalf("metadata error warnings: %v", snap.Warnings)
			}
			if tc.name == "no description" && e.Description != "" {
				t.Fatal("parsed body fallback")
			}
			if tc.name == "crlf" && e.FrontName != "good" {
				t.Fatal("BOM/CRLF header")
			}
		})
	}
	t.Run("document type and vacant", func(t *testing.T) {
		home := t.TempDir()
		pool := filepath.Join(home, ".agents", "skills")
		for _, name := range []string{"bad-document", "vacant", "container", "dangling-document"} {
			if err := os.MkdirAll(filepath.Join(pool, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(pool, "bad-document", "SKILL.md"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pool, "vacant", ".DS_Store"), []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, filepath.Join(pool, "container", "skills", "child"), "child")
		if err := os.Symlink(filepath.Join(home, "missing"), filepath.Join(pool, "dangling-document", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		snap := Scan(Discover(home), nil)
		bad := mustEntry(t, snap, "pool", "bad-document")
		if !bad.HasDocument || bad.MetaErr == "" || bad.Usable() {
			t.Fatalf("bad document: %+v", bad)
		}
		if !mustEntry(t, snap, "pool", "vacant").Vacant {
			t.Fatal("ignored junk is not vacant")
		}
		container := mustEntry(t, snap, "pool", "container")
		if container.Kind != KindEmpty || container.Vacant || len(container.Children) != 0 {
			t.Fatalf("container: %+v", container)
		}
		if mustEntry(t, snap, "pool", "dangling-document").Usable() {
			t.Fatal("dangling document is usable")
		}
	})
}

func TestScanRepo(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "self")
	writeSkill(t, filepath.Join(repo, "direct"), "direct")
	writeSkill(t, filepath.Join(repo, "folder", "nested"), "nested")
	external := t.TempDir()
	writeSkill(t, external, "external")
	linkRel(t, repo, "linked", external)
	snap := Scan(nil, []string{repo})
	id := "repo:" + repo
	entries := snap.EntriesFor(id)
	if len(entries) != 3 {
		t.Fatalf("repo entries: %+v", entries)
	}
	mustEntry(t, snap, id, filepath.Base(repo))
	mustEntry(t, snap, id, "direct")
	if !mustEntry(t, snap, id, "linked").Usable() {
		t.Fatal("direct linked skill missing")
	}
	if _, ok := findEntry(snap, id, "nested"); ok {
		t.Fatal("repo recursive search")
	}
	t.Run("cap", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i <= repoSkillCap; i++ {
			writeSkill(t, filepath.Join(dir, fmt.Sprintf("skill-%03d", i)), "skill")
		}
		snap := Scan(nil, []string{dir})
		if len(snap.Entries) != repoSkillCap || len(snap.Warnings) != 1 {
			t.Fatalf("cap: %d entries, %v", len(snap.Entries), snap.Warnings)
		}
	})
}

func TestCompareCopies(t *testing.T) {
	home := t.TempDir()
	poolDir := filepath.Join(home, ".agents", "skills", "copy")
	sourceDir := filepath.Join(home, ".claude", "skills", "copy")
	writeSkill(t, poolDir, "identical")
	copySkill(t, poolDir, sourceDir)
	snap := Scan(Discover(home), nil)
	source := mustEntry(t, snap, "claude-skills", "copy")
	pool := mustEntry(t, snap, "pool", "copy")
	if source.Relation != RelCopy || source.Relation.Label() != "副本待比较" || source.Relation.Tone() != "warning" {
		t.Fatalf("copy classification: %+v", source)
	}
	if len(snap.Issues) != 0 {
		t.Fatalf("unknown copy issues: %+v", snap.Issues)
	}
	var copies int
	for _, summary := range snap.Summaries {
		copies += summary.Copy
	}
	if copies != 1 {
		t.Fatalf("copy summary: %+v", snap.Summaries)
	}
	same, hashedPool, err := CompareCopies(source, pool)
	if err != nil {
		t.Fatal(err)
	}
	if same.Relation != RelSame || same.TreeHash == "" || same.SkillHash == "" || hashedPool.TreeHash != same.TreeHash {
		t.Fatalf("same comparison: %+v %+v", same, hashedPool)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "attachment.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, freshPool, err := CompareCopies(same, hashedPool)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Relation != RelStale || stale.TreeHash == freshPool.TreeHash || stale.SkillHash != freshPool.SkillHash {
		t.Fatalf("attachment comparison: %+v %+v", stale, freshPool)
	}
	if source.TreeHash != "" || pool.TreeHash != "" {
		t.Fatal("comparison mutated scan entries")
	}
	catalog := FromCatalog(snap.Roots, []Entry{freshPool, stale}, nil, snap.At)
	if mustEntry(t, catalog, "claude-skills", "copy").Relation != RelStale {
		t.Fatal("explicit hashes no longer analyzed")
	}
	writeSkill(t, sourceDir, "fresh metadata")
	changed, _, err := CompareCopies(source, pool)
	if err != nil || changed.Description != "fresh metadata" {
		t.Fatalf("metadata not refreshed: %+v %v", changed, err)
	}
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(poolDir, sourceDir); err != nil {
		t.Fatal(err)
	}
	retargeted, _, err := CompareCopies(stale, freshPool)
	if err == nil || retargeted.TreeHash != "" {
		t.Fatalf("replaced selected slot accepted: %+v %v", retargeted, err)
	}
	if err := os.Remove(sourceDir); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, sourceDir, "fresh")
	if err := os.Remove(filepath.Join(sourceDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	missing, _, err := CompareCopies(stale, freshPool)
	if err == nil || missing.TreeHash != "" || missing.SkillHash != "" {
		t.Fatalf("missing comparison: %+v %v", missing, err)
	}
}

func TestReadDocument(t *testing.T) {
	home := t.TempDir()
	poolDir := filepath.Join(home, ".agents", "skills", "doc")
	agentDir := filepath.Join(home, ".claude", "skills")
	writeSkill(t, poolDir, "document")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRel(t, agentDir, "doc", poolDir)
	snap := Scan(Discover(home), nil)
	e := mustEntry(t, snap, "claude-skills", "doc")
	body := "---\nname: doc\n---\nFull body with `mentions`\n"
	if err := os.WriteFile(filepath.Join(poolDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := ReadDocument(e)
	if err != nil {
		t.Fatal(err)
	}
	if document.Text != body || document.Size != int64(len(body)) || document.ModTime.IsZero() || document.RealPath != e.RealPath {
		t.Fatalf("document: %+v", document)
	}
	if err := os.Remove(filepath.Join(poolDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDocument(e); err == nil {
		t.Fatal("missing document succeeded")
	}
	if _, err := ReadDocument(Entry{}); err == nil {
		t.Fatal("empty path succeeded")
	}
}
