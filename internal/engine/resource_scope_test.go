package engine

import "testing"

func TestPathPatternsMayOverlap(t *testing.T) {
	tests := []struct {
		name        string
		left        string
		right       string
		wantOverlap bool
	}{
		{name: "distinct directories", left: "api/**", right: "web/**", wantOverlap: false},
		{name: "nested directories", left: "internal/**", right: "internal/engine/**", wantOverlap: true},
		{name: "same path", left: "README.md", right: "README.md", wantOverlap: true},
		{name: "distinct files", left: "README.md", right: "CHANGELOG.md", wantOverlap: false},
		{name: "ambiguous root glob", left: "*.md", right: "docs/**", wantOverlap: true},
		{name: "ambiguous nested glob", left: "cmd/**/testdata/**", right: "cmd/api/**", wantOverlap: true},
		{name: "wildcard directory segment", left: "service*/**", right: "service-api/**", wantOverlap: true},
		{name: "wildcard file segment", left: "src/result-*.json", right: "src/result-final.json", wantOverlap: true},
		{name: "workspace wildcard", left: "**", right: "src/**", wantOverlap: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pathPatternsMayOverlap(test.left, test.right); got != test.wantOverlap {
				t.Fatalf("pathPatternsMayOverlap(%q, %q) = %t, want %t", test.left, test.right, got, test.wantOverlap)
			}
		})
	}
}
