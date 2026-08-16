package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStoreUsesGitObjectsAndRefs(t *testing.T) {
	d := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", d}, args...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, b)
		}
	}
	run("init", "-q")
	run("config", "user.name", "test")
	run("config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(d, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "x")
	run("commit", "-qm", "init")

	repo := Repo{Root: d}
	head, _ := repo.Head()
	s := NewStore(repo, "demo")
	if err := s.SetCommit("base", head); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJSON("active", map[string]string{"phase": "01"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	ok, err := s.GetJSON("active", &got)
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if got["phase"] != "01" {
		t.Fatalf("got %#v", got)
	}
	if _, ok, _ := s.Resolve("base"); !ok {
		t.Fatal("base ref missing")
	}
	if err := s.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Resolve("base"); ok {
		t.Fatal("base ref survived reset")
	}
}
