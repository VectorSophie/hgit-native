package tests

import (
	"strings"
	"testing"

	"github.com/VectorSophie/hgit-native/internal/cli"
	"github.com/VectorSophie/hgit-native/internal/testfix"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
	"github.com/VectorSophie/hgit-native/pkg/hgit/views"
)

// TestScenarioDiscoverabilityReplaysRegression replays the regression's last
// block (contract/tests/full-regression.hc lines 370-375): `version`, `logo`,
// then `help` between the TFULL_HELP markers - byte for byte, trailing spaces
// of the logo included.
func TestScenarioDiscoverabilityReplaysRegression(t *testing.T) {
	log := testfix.ExpectedLog(t)
	const after = "TFULL_HARDEN_END_MARKER\n"
	i, j := strings.Index(log, after), strings.Index(log, "TFULL_HELP_BEGIN\n")
	if i < 0 || j < i {
		t.Fatal("markers not found")
	}
	if got, want := cli.Version()+cli.Logo(), log[i+len(after):j]; got != want {
		t.Fatalf("version+logo:\ngot:\n%q\nwant:\n%q", got, want)
	}
	if got, want := cli.Help(), segment(t, log, "TFULL_HELP_BEGIN", "TFULL_HELP_END_MARKER")+"\n"; got != want {
		t.Fatalf("help:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// ported names, per top-level command `help` lists, the package function
// that implements it - so this does not compile if one disappears. The only
// command help lists that has no backend is `interactive`: it switches the
// TempleOS console's screen echo and AutoComplete, which has no native
// counterpart.
var ported = map[string]any{
	"init": repo.Init, "status": status.Status, "offer": offer.Offer, "offertree": offer.OfferTree,
	"merge": merge.Merge, "conflicts": merge.Conflicts, "resolve": merge.Resolve,
	"conflictdoc": views.ConflictDoc, "statustree": status.StatusTree,
	"history": (*repo.Repo).History, "see": (*repo.Repo).See, "diff": status.Diff, "check": check.Run,
	"historydoc": views.HistoryDoc, "reconciledoc": views.ReconcileDoc,
	"reconcileoverview": views.ReconcileOverview, "graph": views.Graph,
	// relation offers are offer.Offer/OfferTree with offer.Options.Relation set
	"correct": offer.Offer, "revert": offer.Offer, "reconcile": offer.Offer,
	"correcttree": offer.OfferTree, "reverttree": offer.OfferTree, "reconciletree": offer.OfferTree,
	"undo": (*repo.Repo).Undo, "redo": (*repo.Repo).Redo, "operation": (*repo.Repo).OperationHistory,
	"path": (*repo.Repo).PathList, "export": repo.Export, "import": repo.Import,
	"help": cli.Help, "version": cli.Version, "logo": cli.Logo,
}

// TestHelpListsExactlyThePortedCommands cross-checks help against the port:
// every command it names is implemented (bar `interactive`, see ported), and
// every implemented command is named.
func TestHelpListsExactlyThePortedCommands(t *testing.T) {
	listed := map[string]bool{}
	for _, line := range strings.Split(cli.Help(), "\n") {
		rest, ok := strings.CutPrefix(line, "HELP_CMD ")
		if !ok || strings.HasPrefix(rest, " ") { // "HELP_CMD   (entity_hex: ...)" is a note
			continue
		}
		listed[strings.Fields(rest)[0]] = true
	}
	for name := range listed {
		if _, ok := ported[name]; !ok && name != "interactive" {
			t.Errorf("help lists %q, which is not ported", name)
		}
	}
	for name := range ported {
		if !listed[name] {
			t.Errorf("%q is ported but help does not list it", name)
		}
	}
}

// TestScenarioViewsOnRegressionRepo runs the four DolDoc views on the
// regression's own TFullRepo (full-regression.hc lines 138-143): each prints
// its DISPATCH_OK line, in the order the log has them right after
// TFULL_SEE_CORRECT_END_MARKER, and the documents carry the golden repo's
// real content.
func TestScenarioViewsOnRegressionRepo(t *testing.T) {
	r := openFixture(t, "TFullRepo.hgs")
	lines, _ := r.History()
	correct := lines[0].Hash

	var got strings.Builder
	docs := map[string]string{}
	for _, v := range []struct {
		name string
		run  func() (string, error)
	}{
		{"historydoc", func() (string, error) { return views.HistoryDoc(r) }},
		{"reconciledoc", func() (string, error) { return views.ReconcileDoc(r, correct) }},
		{"reconcileoverview", func() (string, error) { return views.ReconcileOverview(r) }},
		{"graph", func() (string, error) { return views.Graph(r) }},
	} {
		d, err := v.run()
		if err != nil {
			t.Fatalf("%s: %v", v.name, err)
		}
		docs[v.name] = d
		got.WriteString(cli.SerialView(v.name))
	}
	log := testfix.ExpectedLog(t)
	const after = "TFULL_SEE_CORRECT_END_MARKER\n"
	i := strings.Index(log, after) + len(after)
	if want := strings.Join(strings.SplitAfter(log[i:], "\n")[:4], ""); got.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got.String(), want)
	}

	target := lines[1].Hash.Hex() // the SEE_RELATION target in the log
	if !strings.Contains(log, "SEE_RELATION tag=2 target="+target) {
		t.Fatal("fixture and log disagree on the correct target")
	}
	for name, want := range map[string]string{
		"historydoc":        correct.Hex()[:12] + " 512602 correcting_offer\n",
		"reconciledoc":      "[+] relation: CORRECTS\n      target: " + target + "\n",
		"reconcileoverview": "[+] relation: CORRECTS\n      target: " + target + "\n",
		"graph":             "[+] main, 3 commits\n",
	} {
		if !strings.Contains(docs[name], want) {
			t.Errorf("%s lacks %q:\n%s", name, want, docs[name])
		}
	}
	if !strings.Contains(docs["graph"], "      [+] [feature] ") {
		t.Errorf("graph has no feature branch:\n%s", docs["graph"])
	}
}
