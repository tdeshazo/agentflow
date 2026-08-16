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

func TestStoreNamespacesDoNotCollideAfterGitRefEncoding(t *testing.T) {
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
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	// These names used to normalize to the same ref namespace.
	first := NewStore(repo, "release candidate")
	second := NewStore(repo, "release-candidate")
	if first.Namespace == second.Namespace {
		t.Fatalf("workflow namespaces collide: %q", first.Namespace)
	}
	if err := first.SetCommit("base", head); err != nil {
		t.Fatal(err)
	}
	if err := second.SetCommit("base", head); err != nil {
		t.Fatal(err)
	}
	if err := first.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := first.Resolve("base"); err != nil || ok {
		t.Fatalf("first workflow state survived reset: ok=%v err=%v", ok, err)
	}
	if got, ok, err := second.Resolve("base"); err != nil || !ok || got != head {
		t.Fatalf("second workflow state was affected by first reset: %q ok=%v err=%v", got, ok, err)
	}
}
