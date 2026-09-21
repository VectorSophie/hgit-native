package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

func segment(t *testing.T, log, begin, end string) string {
	t.Helper()
	i := strings.Index(log, begin+"\n")
	j := strings.Index(log, end)
	if i < 0 || j < i {
		t.Fatalf("markers %q..%q not found", begin, end)
	}
	return strings.TrimSpace(log[i+len(begin)+1 : j])
}

var (
	reTS   = regexp.MustCompile(`ts=\d+`)
	reHash = regexp.MustCompile(`\b[0-9a-f]{128}\b`)
	reEnt  = regexp.MustCompile(`\b[0-9A-F]{16}\b`)
)

// normalize masks values that legitimately differ between runs.
func normalize(s string) string {
	s = reTS.ReplaceAllString(s, "ts=T")
	s = reHash.ReplaceAllString(s, "HASH")
	return reEnt.ReplaceAllString(s, "ENTITY")
}

func openFixture(t *testing.T, name string) *repo.Repo {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	os.WriteFile(p, testfix.Read(t, name), 0o644)
	os.WriteFile(p+".m", testfix.Read(t, name+".m"), 0o644)
	r, err := repo.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The fixture is the scenario's final state: `correct` later added a third
// commit on main, so the mid-scenario TFULL_HISTORY segment (2 commits) is
// the tail of the final history. The full replay is left to pillar B.
func TestScenarioHistory(t *testing.T) {
	r := openFixture(t, "TFullRepo.hgs")
	lines, err := r.History()
	got := strings.Split(strings.TrimSpace(cli.SerialHistory(lines, err)), "\n")
	want := strings.Split(segment(t, testfix.ExpectedLog(t), "TFULL_HISTORY_BEGIN", "TFULL_HISTORY_END_MARKER"), "\n")
	// drop the HISTORY_END line from each; compare commit lines as a suffix
	got, want = got[:len(got)-1], want[:len(want)-1]
	if len(got) != len(want)+1 {
		t.Fatalf("got %d commits, want %d+1", len(got), len(want))
	}
	if normalize(strings.Join(got[1:], "\n")) != normalize(strings.Join(want, "\n")) ||
		!strings.HasPrefix(got[0], "commit ts=512602 msg=correcting_offer") {
		t.Fatalf("history mismatch:\n%v\nvs\n%v", got, want)
	}
}

func TestScenarioSee(t *testing.T) {
	r := openFixture(t, "TFullRepo.hgs")
	lines, _ := r.History()
	log := testfix.ExpectedLog(t)
	for _, c := range []struct {
		idx        int
		begin, end string
	}{
		{0, "TFULL_SEE_CORRECT_BEGIN", "TFULL_SEE_CORRECT_END_MARKER"},
		{1, "TFULL_SEE_BEGIN", "TFULL_SEE_END_MARKER"},
	} {
		got := cli.SerialSee(r.See(lines[c.idx].Hash))
		want := segment(t, log, c.begin, c.end)
		if strings.TrimSpace(got) != want { // hashes/ids are deterministic here, compare exactly
			t.Errorf("%s:\n got:\n%s\nwant:\n%s", c.begin, got, want)
		}
	}
}
