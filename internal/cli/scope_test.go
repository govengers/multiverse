package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/mxcd/multiverse/internal/brain"
	"github.com/mxcd/multiverse/internal/config"
)

func mkBrain(t *testing.T, name string) string {
	t.Helper()
	b, err := brain.Init(t.TempDir(), brain.Settings{Name: name, Split: []string{"domain"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	return b.Root
}

func TestBuildScopeSourcesAndTargets(t *testing.T) {
	a := mkBrain(t, "alpha")
	b := mkBrain(t, "beta")
	cfg := &config.Config{}

	// sources = both, targets = alpha only (read both, write one)
	sc, err := buildScope(cfg, &config.Binding{Sources: []string{a, b}, Targets: []string{a}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Sources) != 2 || !sc.multiSource() {
		t.Fatalf("expected 2 sources, got %d", len(sc.Sources))
	}
	target, err := sc.writeTarget()
	if err != nil {
		t.Fatal(err)
	}
	if target.Root != a {
		t.Fatalf("write target should be alpha, got %s", target.Name)
	}
}

func TestTargetsDefaultToSources(t *testing.T) {
	a := mkBrain(t, "alpha")
	sc, err := buildScope(&config.Config{}, &config.Binding{Sources: []string{a}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Targets) != 1 || sc.Targets[0].Root != a {
		t.Fatalf("targets should default to sources, got %+v", sc.Targets)
	}
}

func TestReadOnlyBindingHasNoTargets(t *testing.T) {
	a := mkBrain(t, "alpha")
	// ReadOnly: sources only, no write target.
	sc, err := buildScope(&config.Config{}, &config.Binding{Sources: []string{a}, ReadOnly: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.Sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sc.Sources))
	}
	if len(sc.Targets) != 0 {
		t.Fatalf("read-only scope must have no targets, got %d", len(sc.Targets))
	}
	if _, err := sc.writeTarget(); err == nil {
		t.Fatal("writeTarget must error under a read-only scope")
	}
}

func TestResolveNoteAcrossBrains(t *testing.T) {
	a := mkBrain(t, "alpha")
	b := mkBrain(t, "beta")
	sc, err := buildScope(&config.Config{}, &config.Binding{Sources: []string{a, b}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	// write a uniquely-named note into beta and resolve it from scope
	if _, err := sc.Targets[1].Write(brain.WriteParams{Title: "OnlyInBeta", Dir: "domain", Summary: "s", Tags: []string{"domain"}, Source: "x", Freshness: "y"}); err != nil {
		t.Fatal(err)
	}
	sb, rel, err := sc.resolveNote("OnlyInBeta")
	if err != nil {
		t.Fatal(err)
	}
	if sb.Name != "beta" || rel != "domain/onlyinbeta.md" {
		t.Fatalf("resolved to wrong brain/path: %s %s", sb.Name, rel)
	}

	// a name present in both brains is ambiguous
	for _, sbb := range sc.Sources {
		if _, err := sbb.Write(brain.WriteParams{Title: "Shared", Dir: "domain", Summary: "s", Tags: []string{"domain"}, Source: "x", Freshness: "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := sc.resolveNote("Shared"); err == nil {
		t.Fatal("expected cross-brain ambiguity error")
	}
	// brain:note qualifier disambiguates
	if sb, _, err := sc.resolveNote("alpha:Shared"); err != nil || sb.Name != "alpha" {
		t.Fatalf("qualified resolve failed: %v", err)
	}
}

func TestBrainAliasesResolveInScope(t *testing.T) {
	newRegistry(t)
	if err := runMulti(t, "brain", "add", mkBrain(t, "deep-thought"), "--alias", "dt"); err != nil {
		t.Fatal(err)
	}
	if err := runMulti(t, "brain", "add", mkBrain(t, "pdb-brain"), "--alias", "pdb"); err != nil {
		t.Fatal(err)
	}

	// --brain accepts an alias
	out := captureStdout(t, func() {
		if err := runMulti(t, "--brain", "pdb", "scope"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "sources: pdb-brain (") {
		t.Fatalf("--brain pdb should scope pdb-brain, got:\n%s", out)
	}

	// a committed .multi.yaml naming aliases resolves to the brains
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := config.WriteBinding(dir, config.Binding{Sources: []string{"dt", "pdb"}, Targets: []string{"pdb"}}); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() {
		if err := runMulti(t, "scope"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "sources: deep-thought (") || !strings.Contains(out, "targets: pdb-brain\n") {
		t.Fatalf(".multi.yaml aliases should resolve, got:\n%s", out)
	}

	// brain:note accepts an alias
	sc, err := buildScope(loadConfig(t), &config.Binding{Sources: []string{"dt", "pdb"}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, sb := range sc.Sources {
		if _, err := sb.Write(brain.WriteParams{Title: "Shared", Dir: "domain", Summary: "s", Tags: []string{"domain"}, Source: "x", Freshness: "y"}); err != nil {
			t.Fatal(err)
		}
	}
	if sb, _, err := sc.resolveNote("pdb:Shared"); err != nil || sb.Name != "pdb-brain" {
		t.Fatalf("alias-qualified resolve failed: %v", err)
	}

	// use and scope set store refs as typed: the shared file keeps the team's name
	if err := runMulti(t, "use", "dt", "pdb"); err != nil {
		t.Fatal(err)
	}
	if bnd, err := config.ReadBindingAt(dir); err != nil || !slices.Equal(bnd.Sources, []string{"dt", "pdb"}) {
		t.Fatalf("use should store refs as typed, got %+v (%v)", bnd, err)
	}
	if err := runMulti(t, "scope", "set", "--source", "dt,pdb", "--target", "pdb"); err != nil {
		t.Fatal(err)
	}
	if bnd, err := config.ReadBindingAt(dir); err != nil || !slices.Equal(bnd.Sources, []string{"dt", "pdb"}) || !slices.Equal(bnd.Targets, []string{"pdb"}) {
		t.Fatalf("scope set should store refs as typed, got %+v (%v)", bnd, err)
	}
	if err := runMulti(t, "use", "nope"); err == nil {
		t.Fatal("use must still reject an unknown ref")
	}
}

func TestOpenRefAppliesLocalEmbeddings(t *testing.T) {
	root := mkBrain(t, "alpha")
	cfg := &config.Config{
		Embeddings: &brain.EmbedSettings{Model: "local-model"},
		Brains:     []config.Brain{{Name: "alpha", Path: root, Embeddings: &brain.EmbedSettings{BaseURL: "http://gpu:8090/v1"}}},
	}
	for _, ref := range []string{"alpha", root} { // by name, and by path
		sb, err := openRef(cfg, ref)
		if err != nil {
			t.Fatal(err)
		}
		es := sb.Brain.Embeddings()
		if es == nil || es.BaseURL != "http://gpu:8090/v1" || es.Model != "local-model" {
			t.Errorf("%s: embeddings %+v", ref, es)
		}
	}
}
