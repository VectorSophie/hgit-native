package bundle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/bundle"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

func freezeClock(t *testing.T, start uint64) {
	t.Helper()
	old := clock.Now
	n := start
	clock.Now = func() uint64 { n++; return n }
	t.Cleanup(func() { clock.Now = old })
}

// newRepo initializes a repository at dir/name.hgs and returns it, opened.
func newRepo(t *testing.T, dir, name string) *repo.Repo {
	t.Helper()
	path := filepath.Join(dir, name+".hgs")
	if err := repo.Init(path); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// commitFile writes name=content in dir and offers it on r's current path,
// reopening r's saved state first (as a real CLI invocation would).
func commitFile(t *testing.T, r *repo.Repo, dir, name, content, msg string) archive.Hash {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := offer.Offer(r, dir, name, offer.Options{Message: msg})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	return h
}

// reopen re-reads r's saved files from disk, so a test never accidentally
// asserts on in-memory state a fresh recipient could not see.
func reopen(t *testing.T, r *repo.Repo) *repo.Repo {
	t.Helper()
	r2, err := repo.Open(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	return r2
}

// --- round trips ---------------------------------------------------------

func TestBuildApplyRoundTrip_Baseline(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()

	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	commitFile(t, a, dir, "f.txt", "two", "c2")
	commitFile(t, a, dir, "f.txt", "three", "c3")
	a = reopen(t, a)

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != bundle.KindBaseline {
		t.Fatalf("kind = %d, want baseline", res.Kind)
	}

	b := newRepo(t, dir, "b")
	ar, err := bundle.Apply(b, res.Data, "sender")
	if err != nil {
		t.Fatal(err)
	}
	if ar.NewObjects == 0 {
		t.Fatal("expected new objects on an empty recipient")
	}
	b = reopen(t, b)
	wantHead, _ := a.Head("main")
	gotHead, ok := b.Head("main")
	if !ok || gotHead != wantHead {
		t.Fatalf("main head = %x, want %x", gotHead, wantHead)
	}
}

func TestBuildApplyRoundTrip_Incremental_BothDirections(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()

	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	a = reopen(t, a)

	// b starts as a's replica (baseline bundle), then a advances.
	base, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, base.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)

	commitFile(t, a, dir, "f.txt", "two", "c2")
	commitFile(t, a, dir, "f.txt", "three", "c3")
	a = reopen(t, a)

	have, err := bundle.BuildHave(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	hm, err := bundle.ParseHave(have)
	if err != nil {
		t.Fatal(err)
	}

	incr, err := bundle.Build(a, bundle.BuildOptions{Label: "a", Have: hm})
	if err != nil {
		t.Fatal(err)
	}
	if incr.Kind != bundle.KindIncremental {
		t.Fatalf("kind = %d, want incremental", incr.Kind)
	}
	// (Byte savings depend on how much history the tail carries relative to
	// what came before it; TestByteSavings_IncrementalVsBaseline is the test
	// that measures actual savings on a scenario built for that.)

	ar, err := bundle.Apply(b, incr.Data, "a")
	if err != nil {
		t.Fatal(err)
	}
	if ar.NewObjects == 0 {
		t.Fatal("expected new objects from the incremental bundle")
	}
	b = reopen(t, b)
	wantHead, _ := a.Head("main")
	gotHead, _ := b.Head("main")
	if gotHead != wantHead {
		t.Fatalf("main head = %x, want %x", gotHead, wantHead)
	}

	// Other direction: b makes a path of its own, a pulls it.
	if err := b.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, b, dir, "g.txt", "hi", "feature commit")
	b = reopen(t, b)

	back, err := bundle.Build(b, bundle.BuildOptions{Label: "b", Paths: []string{"feature"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Apply(a, back.Data, "b"); err != nil {
		t.Fatal(err)
	}
	a = reopen(t, a)
	if !a.PathExists("feature") {
		t.Fatal("a did not gain the feature path from b's bundle")
	}
	wantFeat, _ := b.Head("feature")
	gotFeat, _ := a.Head("feature")
	if gotFeat != wantFeat {
		t.Fatalf("feature head = %x, want %x", gotFeat, wantFeat)
	}
}

// --- byte savings ---------------------------------------------------------

func TestByteSavings_IncrementalVsBaseline(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")

	for i := 0; i < 20; i++ {
		commitFile(t, a, dir, "f.txt", string(rune('a'+i))+string(make([]byte, 200)), "commit")
		a = reopen(t, a)
	}

	baseline, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}

	// A replica one commit behind: have-file names 19 of the 20 commits.
	replica := newRepo(t, dir, "replica")
	// Rebuild the replica by applying every commit except the last, via a
	// baseline bundle from an intermediate state is awkward here, so
	// instead just have it claim (via a synthetic have-file) that it holds
	// everything except the very latest commit's own objects: apply the
	// baseline, then have b, an incremental bundle should be near-empty.
	if _, err := bundle.Apply(replica, baseline.Data, "a"); err != nil {
		t.Fatal(err)
	}
	replica = reopen(t, replica)

	commitFile(t, a, dir, "f.txt", "one more edit", "commit 21")
	a = reopen(t, a)

	have, err := bundle.BuildHave(replica, 0)
	if err != nil {
		t.Fatal(err)
	}
	hm, err := bundle.ParseHave(have)
	if err != nil {
		t.Fatal(err)
	}
	incr, err := bundle.Build(a, bundle.BuildOptions{Label: "a", Have: hm})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("baseline bundle: %d bytes (21 commits); incremental bundle: %d bytes (1 commit ahead)", len(baseline.Data), len(incr.Data))
	if len(incr.Data) >= len(baseline.Data)/2 {
		t.Fatalf("incremental bundle (%d bytes) is not meaningfully smaller than baseline (%d bytes)", len(incr.Data), len(baseline.Data))
	}
}

// --- idempotency -----------------------------------------------------------

func TestApplyIsIdempotent(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	commitFile(t, a, dir, "f.txt", "two", "c2")
	a = reopen(t, a)

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	recordsAfterFirst := len(b.Arc.Records)
	headAfterFirst, _ := b.Head("main")

	ar2, err := bundle.Apply(b, res.Data, "a")
	if err != nil {
		t.Fatal(err)
	}
	if ar2.NewObjects != 0 {
		t.Fatalf("re-applying added %d objects, want 0", ar2.NewObjects)
	}
	b = reopen(t, b)
	if len(b.Arc.Records) != recordsAfterFirst {
		t.Fatalf("record count changed on re-apply: %d -> %d", recordsAfterFirst, len(b.Arc.Records))
	}
	head2, _ := b.Head("main")
	if head2 != headAfterFirst {
		t.Fatalf("head moved on re-apply: %x -> %x", headAfterFirst, head2)
	}
}

// --- corruption is refused in Phase 1, nothing written ----------------------

func TestCorruptBundle_FlippedHash(t *testing.T) {
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	a = reopen(t, a)
	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), res.Data...)
	// Flip a byte inside the last object record's stored hash (the 64
	// bytes right before the footer record).
	footerRecStart := len(data) - (1 + 8 + 1 + archive.HashLen + archive.HashLen)
	data[footerRecStart] ^= 0xFF

	b := newRepo(t, dir, "b")
	before := len(b.Arc.Records)
	if _, err := bundle.Apply(b, data, "a"); err == nil {
		t.Fatal("expected a hash-mismatch error")
	}
	b2 := reopen(t, b)
	if len(b2.Arc.Records) != before {
		t.Fatalf("corrupt bundle wrote objects: %d -> %d", before, len(b2.Arc.Records))
	}
}

func TestCorruptBundle_OversizedLength(t *testing.T) {
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	a = reopen(t, a)
	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), res.Data...)
	// The manifest record's length field, right after the 16-byte header.
	for i := 0; i < 8; i++ {
		data[bundle.HeaderLen+i] = 0xFF
	}
	if _, err := bundle.StructuralVerify(data); err == nil {
		t.Fatal("expected a truncation error for an oversized length field")
	}
}

func TestCorruptBundle_TruncatedFooter(t *testing.T) {
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	a = reopen(t, a)
	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	data := res.Data[:len(res.Data)-10]
	b := newRepo(t, dir, "b")
	before := len(b.Arc.Records)
	if _, err := bundle.Apply(b, data, "a"); err == nil {
		t.Fatal("expected an error for a truncated footer")
	}
	b2 := reopen(t, b)
	if len(b2.Arc.Records) != before {
		t.Fatal("truncated bundle wrote objects")
	}
}

func TestCorruptBundle_MissingPrereq(t *testing.T) {
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	h2 := commitFile(t, a, dir, "f.txt", "two", "c2")
	a = reopen(t, a)

	// A hand-built have-manifest claiming an ancestor the recipient never
	// actually holds.
	hm := &bundle.HaveManifest{Heads: []bundle.HeadEntry{{Name: "main", Head: h2}}}
	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a", Have: hm})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != bundle.KindIncremental {
		t.Fatal("expected an incremental bundle naming h2 as prerequisite")
	}

	b := newRepo(t, dir, "b") // empty: does not have h2
	if _, err := bundle.Apply(b, res.Data, "a"); err == nil {
		t.Fatal("expected a missing-prerequisite error")
	}
}

// --- legacy (non-dedup) archives -------------------------------------------

func TestBuild_LegacyAppendArchive(t *testing.T) {
	restore := repo.SetDefaultDedup(false)
	defer restore()
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "same", "c1")
	commitFile(t, a, dir, "f.txt", "same", "c2") // identical content: a duplicate record in legacy mode
	a = reopen(t, a)

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
}

// --- divergent heads ---------------------------------------------------

func TestDivergentHeads_KeptUnderComposedName(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "base", "base")
	a = reopen(t, a)

	base, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, base.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)

	// Both sides commit past the common ancestor.
	commitFile(t, a, dir, "f.txt", "a-side", "a commit")
	a = reopen(t, a)
	commitFile(t, b, dir, "f.txt", "b-side", "b commit")
	b = reopen(t, b)

	localHeadBefore, _ := b.Head("main")

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	ar, err := bundle.Apply(b, res.Data, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(ar.Heads) != 1 || ar.Heads[0].Outcome != bundle.OutcomeKeptDivergent {
		t.Fatalf("expected a kept-divergent outcome, got %+v", ar.Heads)
	}
	b = reopen(t, b)
	localHeadAfter, _ := b.Head("main")
	if localHeadAfter != localHeadBefore {
		t.Fatal("local head moved on a divergent update")
	}
	if !b.PathExists("main@a") {
		t.Fatal("expected main@a to exist")
	}
	incoming, _ := b.Head("main@a")
	wantIncoming, _ := a.Head("main")
	if incoming != wantIncoming {
		t.Fatal("main@a does not hold the incoming head")
	}

	// merge reconciles them.
	if err := b.PathGo("main"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	mergeRes, err := merge.Merge(b, "main@a")
	if err != nil {
		t.Fatal(err)
	}
	_ = mergeRes
}

// --- merge-in-progress and closed paths ------------------------------------

func TestMergeInProgress_NotMoved(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "base", "base")
	a = reopen(t, a)

	base, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, base.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)

	if err := b.PathNew("side"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.PathGo("side"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, b, dir, "f.txt", "side-edit", "side commit")
	b = reopen(t, b)

	// Start (and leave open) a merge of "side" into "main". Both sides must
	// have diverged from their common ancestor (an edit on main too) or
	// Merge would just fast-forward instead of leaving a real merge with
	// conflicts in progress - main itself is the path this test wants a
	// merge in progress on.
	if err := b.PathGo("main"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	commitFile(t, b, dir, "f.txt", "main-edit", "main commit")
	b = reopen(t, b)
	if _, err := merge.Merge(b, "side"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	if _, ok := b.Meta.Find("main", meta.TagMergeState); !ok {
		t.Fatal("test setup: expected a merge in progress on main")
	}

	commitFile(t, a, dir, "f.txt", "a-edit", "a commit")
	a = reopen(t, a)
	localHeadBefore, _ := b.Head("main")

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Apply(b, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	localHeadAfter, _ := b.Head("main")
	if localHeadAfter != localHeadBefore {
		t.Fatal("main moved despite an in-progress merge")
	}
	if !b.PathExists("main@a") {
		t.Fatal("expected the incoming head recorded under main@a")
	}
}

func TestClosedPath_NotReopened(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "base", "base")
	a = reopen(t, a)

	base, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, base.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)

	if err := a.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := a.PathGo("feature"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, a, dir, "f.txt", "feature-edit", "feature commit")
	a = reopen(t, a)

	if err := b.PathNew("feature"); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if err := b.PathClose("feature"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	if b.PathExists("feature") {
		t.Fatal("test setup: feature should be closed")
	}

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Apply(b, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	if b.PathExists("feature") {
		t.Fatal("a closed path must not be reopened by an incoming update")
	}
	if !b.PathExists("feature@a") {
		t.Fatal("expected the incoming head recorded under feature@a instead")
	}
}

// --- selected-path exchange --------------------------------------------

func TestSelectedPaths_LimitsExchange(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "base", "base")
	a = reopen(t, a)
	if err := a.PathNew("other"); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	if err := a.PathGo("other"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, a, dir, "g.txt", "other-content", "other commit")
	a = reopen(t, a)

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a", Paths: []string{"main"}})
	if err != nil {
		t.Fatal(err)
	}
	sv, err := bundle.StructuralVerify(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(sv.Manifest.Heads) != 1 || sv.Manifest.Heads[0].Name != "main" {
		t.Fatalf("expected only main proposed, got %+v", sv.Manifest.Heads)
	}

	b := newRepo(t, dir, "b")
	if _, err := bundle.Apply(b, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
	b = reopen(t, b)
	if b.PathExists("other") {
		t.Fatal("--paths main must not have proposed the other path")
	}
}

// --- interruption: a crash between Phase 2's two atomic writes -------------

// TestInterruptedApply_ArchiveWrittenMetaNot simulates a crash between
// Save's two atomic writes (archive first, then metadata, per repo.Save's
// own doc comment): the recipient ends up with new objects but a head that
// never moved. That is exactly the "dangling, harmless, completed by
// re-running" case BUNDLE.md describes, not a corrupt repository.
func TestInterruptedApply_ArchiveWrittenMetaNot(t *testing.T) {
	freezeClock(t, 1_700_000_000_000)
	dir := t.TempDir()
	a := newRepo(t, dir, "a")
	commitFile(t, a, dir, "f.txt", "one", "c1")
	commitFile(t, a, dir, "f.txt", "two", "c2")
	a = reopen(t, a)

	res, err := bundle.Build(a, bundle.BuildOptions{Label: "a"})
	if err != nil {
		t.Fatal(err)
	}

	b := newRepo(t, dir, "b")
	v, err := bundle.Verify(res.Data, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range v.Objects {
		b.Store(rec.Type(), rec.Content())
	}
	// Simulate the crash: only the archive half of Save reaches disk.
	if err := os.WriteFile(b.Path, b.Arc.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}

	crashed, err := repo.Open(b.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := crashed.Head("main"); ok {
		t.Fatal("head must not have moved before the crash")
	}
	rep := check.Run(crashed)
	if len(rep.Dangling) == 0 {
		t.Fatal("expected dangling objects after the interrupted apply")
	}
	if len(rep.HashBad) != 0 || rep.RefsBroken != 0 {
		t.Fatalf("interrupted apply must not corrupt what was written: %+v", rep)
	}

	// A re-run completes it.
	if _, err := bundle.Apply(crashed, res.Data, "a"); err != nil {
		t.Fatal(err)
	}
	final := reopen(t, crashed)
	head, ok := final.Head("main")
	wantHead, _ := a.Head("main")
	if !ok || head != wantHead {
		t.Fatal("re-running the apply did not complete it")
	}
	finalRep := check.Run(final)
	if len(finalRep.Dangling) != 0 {
		t.Fatalf("expected no dangling objects once the head catches up, got %+v", finalRep.Dangling)
	}
}
