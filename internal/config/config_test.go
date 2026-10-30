package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReposRoundTrip(t *testing.T) {
	t.Setenv("MULTI_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Add(Brain{Name: "alpha", Path: "/vaults/alpha"})
	c.Platforms = map[string]string{"github.com": "github"}
	c.Repos = map[string]Repo{
		"github/mxcd/multiverse": {Path: "/src/multiverse", Aliases: []string{"multi"}},
		"github/fs-us/iac":       {Path: "/src/iac"},
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if r := got.Repos["github/mxcd/multiverse"]; r.Path != "/src/multiverse" || !slices.Equal(r.Aliases, []string{"multi"}) || len(got.Repos) != 2 {
		t.Fatalf("repos not round-tripped: %+v", got.Repos)
	}
	if got.Platforms["github.com"] != "github" {
		t.Fatalf("platforms not round-tripped: %+v", got.Platforms)
	}
	if got.Find("alpha") == nil {
		t.Fatal("brains lost next to repos")
	}
}

func TestLegacyScalarReposLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MULTI_CONFIG_DIR", dir)
	// the v1.6 form: repo id -> plain path string
	legacy := "repos:\n    cluster-csi: /src/cluster-csi\n    mbag/cluster-iam: /src/cluster-iam\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Repos["cluster-csi"].Path != "/src/cluster-csi" || c.Repos["mbag/cluster-iam"].Path != "/src/cluster-iam" {
		t.Fatalf("legacy repos not loaded: %+v", c.Repos)
	}
}

func TestConfigWithoutReposSavesUnchanged(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MULTI_CONFIG_DIR", dir)
	// exactly what yaml.v3 writes for a pre-repos config
	orig := "active: alpha\nbrains:\n    - name: alpha\n      path: /vaults/alpha\n"
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != orig {
		t.Fatalf("config changed on save:\n%s", data)
	}
}

func TestFindBrainByAlias(t *testing.T) {
	c := &Config{Brains: []Brain{
		{Name: "deep-thought", Path: "/vaults/deep-thought", Aliases: []string{"dt"}},
		{Name: "pdb-brain", Path: "/vaults/pdb-brain", Aliases: []string{"pdb"}},
	}}
	for ref, want := range map[string]string{"deep-thought": "deep-thought", "dt": "deep-thought", "pdb": "pdb-brain"} {
		if b := c.Find(ref); b == nil || b.Name != want {
			t.Errorf("Find(%q) = %+v, want %s", ref, b, want)
		}
	}
	if c.Find("unknown") != nil || c.AliasOwner("deep-thought") != nil {
		t.Fatal("a name is not an alias, an unknown ref finds nothing")
	}
	// a name wins over an alias (only reachable by hand-editing)
	c.Brains = append(c.Brains, Brain{Name: "dt", Path: "/vaults/dt"})
	if b := c.Find("dt"); b.Path != "/vaults/dt" {
		t.Fatalf("name must win over alias, got %+v", b)
	}
	// Add matches names only: an alias never has its brain's path replaced
	c.Add(Brain{Name: "pdb", Path: "/vaults/pdb"})
	if c.Find("pdb-brain").Path != "/vaults/pdb-brain" || len(c.Brains) != 4 {
		t.Fatalf("Add by an alias must not touch its owner: %+v", c.Brains)
	}
}

func TestBrainAliasesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MULTI_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Brains = []Brain{{Name: "deep-thought", Path: "/vaults/deep-thought", Aliases: []string{"dt", "brain"}}, {Name: "alpha", Path: "/vaults/alpha"}}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := "brains:\n    - name: deep-thought\n      path: /vaults/deep-thought\n      aliases: [dt, brain]\n    - name: alpha\n      path: /vaults/alpha\n"
	if string(data) != want {
		t.Fatalf("unexpected config:\n%s", data)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Find("dt").Aliases, []string{"dt", "brain"}) || got.Find("alpha").Aliases != nil {
		t.Fatalf("aliases not round-tripped: %+v", got.Brains)
	}
}

func TestActiveAliasLoadsAsName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MULTI_CONFIG_DIR", dir)
	hand := "active: dt\nbrains:\n    - name: deep-thought\n      path: /vaults/deep-thought\n      aliases: [dt]\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Active != "deep-thought" || c.ActiveBrain() == nil {
		t.Fatalf("active alias should load as the brain name, got %q", c.Active)
	}
}

func TestLocalEmbeddings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MULTI_CONFIG_DIR", dir)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`embeddings:
    base_url: http://gpu:8090/v1
brains:
    - name: csi
      path: /b/csi
      embeddings:
        model: qwen3-embedding-4b
    - name: pdb
      path: /b/pdb
`), 0o644)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LocalEmbeddings(c.Find("csi")); got.BaseURL != "http://gpu:8090/v1" || got.Model != "qwen3-embedding-4b" {
		t.Errorf("csi: %+v; the brain's entry goes over the registry-wide block", got)
	}
	if got := c.LocalEmbeddings(c.Find("pdb")); got.BaseURL != "http://gpu:8090/v1" || got.Model != "" {
		t.Errorf("pdb: %+v; only the registry-wide block applies", got)
	}
	if got := c.LocalEmbeddings(nil); got != c.Embeddings {
		t.Error("an unregistered brain gets the registry-wide block")
	}
	if c.FindPath("/b/csi") == nil || c.FindPath("/b/other") != nil {
		t.Error("FindPath")
	}
}
