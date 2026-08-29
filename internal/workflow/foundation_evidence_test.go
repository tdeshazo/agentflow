package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestFoundationClosureEvidenceCoversEveryRoadmapCriterion(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate foundation evidence test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	roadmap := mustReadEvidenceFile(t, filepath.Join(root, "ROADMAP.md"))
	review := mustReadEvidenceFile(t, filepath.Join(root, "docs", "reviews", "foundation-closure-audit.md"))
	evidence := mustReadEvidenceFile(t, filepath.Join(root, "docs", "evidence", "foundation-closure.md"))

	groups := map[string]int{
		"1A-S": 8, "1A-E": 4,
		"1B-S": 8, "1B-E": 3,
		"1C-RT": 6, "1C-PG": 6, "1C-EV": 6, "1C-AU": 7, "1C-PL": 4, "1C-E": 10,
	}
	for prefix, count := range groups {
		for i := 1; i <= count; i++ {
			id := prefix + strconv.Itoa(i)
			if !strings.Contains(review, "| "+id+" |") {
				t.Errorf("foundation review is missing roadmap mapping %s", id)
			}
			if strings.HasSuffix(prefix, "-E") && !strings.Contains(evidence, "### "+id+" ") {
				t.Errorf("foundation evidence is missing exit criterion %s", id)
			}
		}
	}

	roadmapCounts := stageOneRoadmapCounts(t, roadmap)
	mappedCounts := map[string][2]int{
		"1A": {groups["1A-S"], groups["1A-E"]},
		"1B": {groups["1B-S"], groups["1B-E"]},
		"1C": {groups["1C-RT"] + groups["1C-PG"] + groups["1C-EV"] + groups["1C-AU"] + groups["1C-PL"], groups["1C-E"]},
	}
	for stage, mapped := range mappedCounts {
		if roadmapCounts[stage] != mapped {
			t.Errorf("%s audit mapping covers scope/exit counts %v, ROADMAP.md declares %v", stage, mapped, roadmapCounts[stage])
		}
	}
}

func TestFoundationClosureEvidenceUsesReproducibleRevisionBinding(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate foundation evidence test source")
	}
	root := filepath.Join(filepath.Dir(source), "..", "..")
	evidence := mustReadEvidenceFile(t, filepath.Join(root, "docs", "evidence", "foundation-closure.md"))
	staleHashClaim := regexp.MustCompile("(?m)^Evidence baseline: repository (?:`?HEAD`? )?`?[0-9a-f]{7,40}`?$")
	if staleHashClaim.MatchString(evidence) {
		t.Fatal("foundation evidence embeds a self-invalidating repository HEAD claim")
	}
	for _, required := range []string{"./scripts/check.sh", "-count=1", "ROADMAP.md"} {
		if !strings.Contains(evidence, required) {
			t.Errorf("foundation evidence is not tied to deterministic replay; missing %q", required)
		}
	}
}

func mustReadEvidenceFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func stageOneRoadmapCounts(t *testing.T, roadmap string) map[string][2]int {
	t.Helper()
	result := map[string][2]int{}
	for _, stage := range []string{"1A", "1B", "1C"} {
		startMarker := "### Stage " + stage + " —"
		start := strings.Index(roadmap, startMarker)
		if start < 0 {
			t.Fatalf("ROADMAP.md is missing %s", startMarker)
		}
		section := roadmap[start:]
		if next := strings.Index(section[len(startMarker):], "\n### Stage "); next >= 0 {
			section = section[:len(startMarker)+next]
		} else if next := strings.Index(section, "\n## Execution stage 2"); next >= 0 {
			section = section[:next]
		}
		scopeStart := strings.Index(section, "#### Scope")
		exitStart := strings.Index(section, "#### Exit criteria")
		if scopeStart < 0 || exitStart < 0 || exitStart <= scopeStart {
			t.Fatalf("ROADMAP.md %s section is missing ordered scope/exit headings", stage)
		}
		countBullets := func(body string) int {
			count := 0
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "- ") {
					count++
				}
			}
			return count
		}
		result[stage] = [2]int{
			countBullets(section[scopeStart:exitStart]),
			countBullets(section[exitStart:]),
		}
	}
	return result
}
