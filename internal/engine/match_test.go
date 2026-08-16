package engine

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		p, n string
		want bool
	}{
		{"internal/**", "internal/a/b.go", true},
		{"internal/game/*", "internal/game/x.go", true},
		{"internal/game/*", "internal/game/sub/x.go", false},
		{"README.md", "README.md", true},
	}
	for _, c := range cases {
		if got := globMatch(c.p, c.n); got != c.want {
			t.Fatalf("globMatch(%q,%q)=%v want %v", c.p, c.n, got, c.want)
		}
	}
}
