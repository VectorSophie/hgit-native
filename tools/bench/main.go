// Command bench is the reproducible corpus and benchmark harness for the
// storage and exchange work. It drives the real library API exactly as the
// command line does (repo.Open, then the command, then Save), on
// deterministic repositories: a seeded PRNG, a frozen monotonic clock and
// counter entity ids make every run build byte-identical repositories.
//
//	go run ./tools/bench -scenario s1 -out results.jsonl
//	go run ./tools/bench -report results.jsonl
//	go run ./tools/bench -report before.jsonl,after.jsonl
//
// Scenarios (see docs/benchmarks/README.md for the methodology):
//
//	s1  hot files      few files, many successive commits, one file edited each
//	s2  wide tree      many files across many directories (offertree)
//	s3  identical      many files sharing a handful of distinct contents
//	s4  paths+merges   divergent named paths merged back
//	s5  large binary   multi-megabyte incompressible files, one block edited
//	s6  replica lag    a replica 1/10/1000 commits behind: whole-file transfer
//	                   versus the exact bytes of objects the replica lacks
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/check"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/merge"
	"github.com/VectorSophie/hgit-native/pkg/hgit/offer"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
	"github.com/VectorSophie/hgit-native/pkg/hgit/status"
)

// Sample is one measurement point: a repository at a commit count.
type Sample struct {
	Scenario string `json:"scenario"`
	Variant  string `json:"variant,omitempty"`
	Commits  int    `json:"commits"`

	HgsBytes   int64 `json:"hgs_bytes"`
	MetaBytes  int64 `json:"meta_bytes"`
	Records    int   `json:"records"`
	Unique     int   `json:"unique_objects"`
	FloorBytes int64 `json:"unique_floor_bytes"` // size if every object were stored once
	WorkBytes  int64 `json:"working_set_bytes"`

	OfferMs   float64 `json:"offer_ms"`   // the offer that produced this commit, excluding open
	OpenMs    float64 `json:"open_ms"`    // repo.Open
	SaveMs    float64 `json:"save_ms"`    // Open + Save with no change
	StatusMs  float64 `json:"status_ms"`  // status on a clean tree
	HistoryMs float64 `json:"history_ms"` // full first-parent walk
	CheckMs   float64 `json:"check_ms"`   // check.Run
	OpenAlloc uint64  `json:"open_alloc_bytes"`
	OpenHeap  uint64  `json:"open_heap_bytes"` // live heap after Open
	Note      string  `json:"note,omitempty"`

	// Replica-lag scenario only.
	Behind        int   `json:"behind,omitempty"`
	WholeTransfer int64 `json:"whole_transfer_bytes,omitempty"` // export/import copies both files
	FloorTransfer int64 `json:"floor_transfer_bytes,omitempty"` // bytes of records the replica lacks
	MissingObjs   int   `json:"missing_objects,omitempty"`
}

type env struct {
	Go, OS, Arch string
	CPUs         int
	Dir          string
	Commit       string
	Date         string
}

var (
	flagScenario = flag.String("scenario", "all", "s1..s6 or all")
	flagOut      = flag.String("out", "", "append JSON lines here (default stdout)")
	flagDir      = flag.String("dir", "", "working directory root (default: a temp dir)")
	flagRuns     = flag.Int("runs", 3, "timing repetitions; the median is reported")
	flagQuick    = flag.Bool("quick", false, "smaller sizes for a smoke run")
	flagReport   = flag.String("report", "", "render JSON-lines results file(s) as markdown and exit; several, comma-separated, are compared row by row")
	flagVariant  = flag.String("variant", "", "label stored in every sample (e.g. baseline, dedup)")
)

func main() {
	flag.Parse()
	if *flagReport != "" {
		if err := report(*flagReport); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	root := *flagDir
	if root == "" {
		d, err := os.MkdirTemp("", "hgit-bench-")
		must(err)
		defer os.RemoveAll(d)
		root = d
	}
	out := os.Stdout
	if *flagOut != "" {
		f, err := os.OpenFile(*flagOut, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		must(err)
		defer f.Close()
		out = f
	}
	emit := func(s Sample) {
		s.Variant = *flagVariant
		b, _ := json.Marshal(s)
		fmt.Fprintln(out, string(b))
		fmt.Fprintf(os.Stderr, "%-3s commits=%-5d hgs=%-11d records=%-7d unique=%-6d open=%.1fms offer=%.1fms check=%.1fms\n",
			s.Scenario, s.Commits, s.HgsBytes, s.Records, s.Unique, s.OpenMs, s.OfferMs, s.CheckMs)
	}
	which := strings.Split(*flagScenario, ",")
	want := func(n string) bool {
		for _, w := range which {
			if w == "all" || w == n {
				return true
			}
		}
		return false
	}
	fmt.Fprintf(os.Stderr, "bench: go=%s %s/%s cpus=%d dir=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), root)
	if want("s1") {
		s1(root, emit)
	}
	if want("s2") {
		s2(root, emit)
	}
	if want("s3") {
		s3(root, emit)
	}
	if want("s4") {
		s4(root, emit)
	}
	if want("s5") {
		s5(root, emit)
	}
	if want("s6") {
		s6(root, emit)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

// ---- determinism -----------------------------------------------------------

func deterministic() {
	var id uint64
	offer.NewEntityID = func() uint64 { id++; return id }
	var t uint64
	clock.Now = func() uint64 { t++; return 1_700_000_000_000 + t }
}

var words = strings.Fields(`the quick brown fox jumps over a lazy dog while archive object commit path
merge entity relation conflict evidence history offer status graph reconcile record hash tree blob
version storage replica bundle prerequisite manifest verify apply frontier ancestor descendant`)

// textFile returns n bytes of deterministic pseudo-prose.
func textFile(rng *rand.Rand, n int) []byte {
	var b strings.Builder
	for b.Len() < n {
		for w := 0; w < 9; w++ {
			b.WriteString(words[rng.Intn(len(words))])
			b.WriteByte(' ')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()[:n])
}

func randomBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	rng.Read(b)
	return b
}

// editText changes one line in the middle of the file (a realistic small edit).
func editText(rng *rand.Rand, b []byte) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	i := rng.Intn(len(lines))
	lines[i] = words[rng.Intn(len(words))] + " edited " + fmt.Sprint(rng.Intn(1e6)) + "\n"
	return []byte(strings.Join(lines, ""))
}

// ---- measurement -----------------------------------------------------------

func median(f func() time.Duration) float64 {
	n := *flagRuns
	ds := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		ds = append(ds, float64(f().Microseconds())/1000)
	}
	sort.Float64s(ds)
	return ds[len(ds)/2]
}

func workBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".hgs") && !strings.HasSuffix(p, ".hgs.m") && !strings.Contains(p, ".tmp") {
			if fi, e := d.Info(); e == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// measure fills a Sample for the repository at repoPath / work tree.
func measure(s *Sample, repoPath, work string, tree bool) {
	fi, _ := os.Stat(repoPath)
	s.HgsBytes = fi.Size()
	if mi, err := os.Stat(repoPath + ".m"); err == nil {
		s.MetaBytes = mi.Size()
	}
	s.WorkBytes = workBytes(work)

	r, err := repo.Open(repoPath)
	must(err)
	seen := map[archive.Hash]bool{}
	s.Records = len(r.Arc.Records)
	s.FloorBytes = archive.HeaderLen
	for _, rec := range r.Arc.Records {
		if !seen[rec.Hash] {
			seen[rec.Hash] = true
			s.FloorBytes += int64(8 + len(rec.Data) + archive.HashLen)
		}
	}
	s.Unique = len(seen)

	s.OpenMs = median(func() time.Duration {
		t := time.Now()
		_, err := repo.Open(repoPath)
		must(err)
		return time.Since(t)
	})
	// Allocation and live heap of one Open.
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	held, err := repo.Open(repoPath)
	must(err)
	runtime.ReadMemStats(&b)
	s.OpenAlloc = b.TotalAlloc - a.TotalAlloc
	runtime.GC()
	runtime.ReadMemStats(&b)
	s.OpenHeap = b.HeapAlloc - a.HeapAlloc
	runtime.KeepAlive(held)

	// Save with no change: writes the whole archive back.
	scratch := repoPath + ".savebench"
	s.SaveMs = median(func() time.Duration {
		rr, err := repo.Open(repoPath)
		must(err)
		rr.Path = scratch
		t := time.Now()
		must(rr.Save())
		return time.Since(t)
	})
	os.Remove(scratch)
	os.Remove(scratch + ".m")

	s.StatusMs = median(func() time.Duration {
		rr, err := repo.Open(repoPath)
		must(err)
		t := time.Now()
		if tree {
			_, err = status.StatusTree(rr, work)
		} else {
			_, err = status.Status(rr, work, "*")
		}
		must(err)
		return time.Since(t)
	})
	s.HistoryMs = median(func() time.Duration {
		rr, err := repo.Open(repoPath)
		must(err)
		t := time.Now()
		_, err = rr.History()
		must(err)
		return time.Since(t)
	})
	s.CheckMs = median(func() time.Duration {
		rr, err := repo.Open(repoPath)
		must(err)
		t := time.Now()
		check.Run(rr)
		return time.Since(t)
	})
}

// commit runs one offer the way the command line does and returns the time of
// the offer itself (open excluded, Save included).
func commit(repoPath, work string, tree bool, msg string) float64 {
	r, err := repo.Open(repoPath)
	must(err)
	t := time.Now()
	if tree {
		_, err = offer.OfferTree(r, work, offer.Options{Message: msg})
	} else {
		_, err = offer.Offer(r, work, "*", offer.Options{Message: msg})
	}
	must(err)
	return float64(time.Since(t).Microseconds()) / 1000
}

func fresh(root, name string) (repoPath, work string) {
	base := filepath.Join(root, name)
	os.RemoveAll(base)
	must(os.MkdirAll(base, 0o755))
	work = filepath.Join(base, "work")
	must(os.MkdirAll(work, 0o755))
	repoPath = filepath.Join(base, "R.hgs")
	must(repo.Init(repoPath))
	return
}

func write(p string, b []byte) {
	must(os.MkdirAll(filepath.Dir(p), 0o755))
	must(os.WriteFile(p, b, 0o644))
}

func has(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ---- scenarios -------------------------------------------------------------

func s1(root string, emit func(Sample)) {
	deterministic()
	rng := rand.New(rand.NewSource(1))
	checkpoints := []int{1, 10, 100, 300, 1000}
	if *flagQuick {
		checkpoints = []int{1, 10, 30}
	}
	repoPath, work := fresh(root, "s1")
	const files = 5
	content := map[string][]byte{}
	for i := 0; i < files; i++ {
		n := fmt.Sprintf("f%d.txt", i)
		content[n] = textFile(rng, 4096)
		write(filepath.Join(work, n), content[n])
	}
	last := checkpoints[len(checkpoints)-1]
	for c := 1; c <= last; c++ {
		n := fmt.Sprintf("f%d.txt", c%files)
		content[n] = editText(rng, content[n])
		write(filepath.Join(work, n), content[n])
		ms := commit(repoPath, work, false, fmt.Sprintf("c%d", c))
		if has(checkpoints, c) {
			s := Sample{Scenario: "s1", Commits: c, OfferMs: ms}
			measure(&s, repoPath, work, false)
			emit(s)
		}
	}
}

func s2(root string, emit func(Sample)) {
	type cfg struct{ files, dirs, commits int }
	cfgs := []cfg{{2000, 100, 30}, {10000, 200, 5}}
	if *flagQuick {
		cfgs = []cfg{{200, 20, 10}}
	}
	for _, cf := range cfgs {
		deterministic()
		rng := rand.New(rand.NewSource(2))
		name := fmt.Sprintf("s2-%d", cf.files)
		repoPath, work := fresh(root, name)
		var names []string
		for i := 0; i < cf.files; i++ {
			names = append(names, filepath.Join(work, fmt.Sprintf("d%03d", i%cf.dirs), fmt.Sprintf("f%05d.txt", i)))
			write(names[i], textFile(rng, 1024+rng.Intn(1024)))
		}
		for c := 1; c <= cf.commits; c++ {
			for e := 0; e < 3; e++ { // three files edited per commit
				p := names[rng.Intn(len(names))]
				b, _ := os.ReadFile(p)
				write(p, editText(rng, b))
			}
			ms := commit(repoPath, work, true, fmt.Sprintf("c%d", c))
			if c == 1 || c == cf.commits || c == cf.commits/2 {
				s := Sample{Scenario: "s2", Commits: c, OfferMs: ms, Note: fmt.Sprintf("%d files in %d dirs", cf.files, cf.dirs)}
				measure(&s, repoPath, work, true)
				emit(s)
			}
		}
	}
}

func s3(root string, emit func(Sample)) {
	deterministic()
	rng := rand.New(rand.NewSource(3))
	files, distinct, commits := 500, 8, 20
	if *flagQuick {
		files, commits = 100, 5
	}
	repoPath, work := fresh(root, "s3")
	pool := make([][]byte, distinct)
	for i := range pool {
		pool[i] = textFile(rng, 2048)
	}
	for i := 0; i < files; i++ {
		write(filepath.Join(work, fmt.Sprintf("f%04d.txt", i)), pool[i%distinct])
	}
	for c := 1; c <= commits; c++ {
		p := filepath.Join(work, fmt.Sprintf("f%04d.txt", rng.Intn(files)))
		b, _ := os.ReadFile(p)
		write(p, editText(rng, b))
		ms := commit(repoPath, work, false, fmt.Sprintf("c%d", c))
		if c == 1 || c == commits {
			s := Sample{Scenario: "s3", Commits: c, OfferMs: ms, Note: fmt.Sprintf("%d files, %d distinct contents", files, distinct)}
			measure(&s, repoPath, work, false)
			emit(s)
		}
	}
}

func s4(root string, emit func(Sample)) {
	deterministic()
	rng := rand.New(rand.NewSource(4))
	base, side, sidesN := 50, 10, 5
	if *flagQuick {
		base, side, sidesN = 10, 4, 2
	}
	repoPath, work := fresh(root, "s4")
	const files = 8
	for i := 0; i < files; i++ {
		write(filepath.Join(work, fmt.Sprintf("m%d.txt", i)), textFile(rng, 2048))
	}
	commits := 0
	do := func(msg string) {
		commits++
		commit(repoPath, work, false, msg)
	}
	do("root")
	for c := 1; c < base; c++ {
		p := filepath.Join(work, fmt.Sprintf("m%d.txt", c%files))
		b, _ := os.ReadFile(p)
		write(p, editText(rng, b))
		do(fmt.Sprintf("main%d", c))
	}
	// Each side path forks from the tip and touches its own file only. Main
	// then advances by one commit before the merge, so every merge is a true
	// clean three-way merge (not a fast-forward) that writes a merge commit.
	for sIdx := 0; sIdx < sidesN; sIdx++ {
		name := fmt.Sprintf("side%d", sIdx)
		sidePath := filepath.Join(work, fmt.Sprintf("side%d.txt", sIdx))
		r, err := repo.Open(repoPath)
		must(err)
		must(r.PathNew(name))
		must(r.PathGo(name))
		must(r.Save())
		var sideContent []byte
		for c := 0; c < side; c++ {
			sideContent = textFile(rng, 1500+c)
			write(sidePath, sideContent)
			do(fmt.Sprintf("%s-%d", name, c))
		}
		r, err = repo.Open(repoPath)
		must(err)
		must(r.PathGo("main"))
		must(r.Save())
		// Main's working tree has no side file; advance main by one commit.
		os.Remove(sidePath)
		p0 := filepath.Join(work, "m0.txt")
		b0, _ := os.ReadFile(p0)
		write(p0, editText(rng, b0))
		do(fmt.Sprintf("main-after-fork-%d", sIdx))
		r, err = repo.Open(repoPath)
		must(err)
		t := time.Now()
		res, err := merge.Merge(r, name)
		must(err)
		if res.FastForward || res.UpToDate || len(res.Conflicts) > 0 {
			must(fmt.Errorf("s4: merge %s was not a clean three-way merge: %+v", name, res))
		}
		ms := float64(time.Since(t).Microseconds()) / 1000
		commits++
		write(sidePath, sideContent) // the working tree follows the merge result
		if sIdx == sidesN-1 {
			s := Sample{Scenario: "s4", Commits: commits, OfferMs: ms, Note: fmt.Sprintf("%d side paths merged; offer_ms is the last merge", sidesN)}
			measure(&s, repoPath, work, false)
			emit(s)
		}
	}
}

func s5(root string, emit func(Sample)) {
	deterministic()
	rng := rand.New(rand.NewSource(5))
	size, nfiles, commits := 2<<20, 5, 10
	if *flagQuick {
		size, nfiles, commits = 256<<10, 3, 4
	}
	repoPath, work := fresh(root, "s5")
	content := make([][]byte, nfiles)
	for i := range content {
		content[i] = randomBytes(rng, size)
		write(filepath.Join(work, fmt.Sprintf("blob%d.bin", i)), content[i])
	}
	for c := 1; c <= commits; c++ {
		i := c % nfiles
		copy(content[i][size/2:size/2+65536], randomBytes(rng, 65536)) // one 64 KiB block changes
		write(filepath.Join(work, fmt.Sprintf("blob%d.bin", i)), content[i])
		ms := commit(repoPath, work, false, fmt.Sprintf("c%d", c))
		if c == 1 || c == commits {
			s := Sample{Scenario: "s5", Commits: c, OfferMs: ms, Note: fmt.Sprintf("%d x %d KiB binary, 64 KiB edited per commit", nfiles, size>>10)}
			measure(&s, repoPath, work, false)
			emit(s)
		}
	}
}

// s6 measures what a replica that is k commits behind must be sent. The
// whole-repository export/import copies both files regardless of k; the floor
// is the exact byte size of the records whose hash the replica does not hold.
func s6(root string, emit func(Sample)) {
	deterministic()
	rng := rand.New(rand.NewSource(6))
	total := 1000
	if *flagQuick {
		total = 40
	}
	behinds := []int{1, 10, 1000}
	if *flagQuick {
		behinds = []int{1, 10, 30}
	}
	repoPath, work := fresh(root, "s6")
	const files = 5
	content := map[string][]byte{}
	for i := 0; i < files; i++ {
		n := fmt.Sprintf("f%d.txt", i)
		content[n] = textFile(rng, 4096)
		write(filepath.Join(work, n), content[n])
	}
	snapAt := map[int]int{}          // behind-k -> commit index the replica is at
	snap := map[int][]archive.Hash{} // behind-k -> hashes held at that point
	for _, k := range behinds {
		ck := total - k
		if ck < 1 {
			ck = 1
		}
		snapAt[k] = ck
	}
	for c := 1; c <= total; c++ {
		n := fmt.Sprintf("f%d.txt", c%files)
		content[n] = editText(rng, content[n])
		write(filepath.Join(work, n), content[n])
		commit(repoPath, work, false, fmt.Sprintf("c%d", c))
		for _, k := range behinds {
			if c == snapAt[k] {
				r, err := repo.Open(repoPath)
				must(err)
				hs := make([]archive.Hash, 0, len(r.Arc.Records))
				for _, rec := range r.Arc.Records {
					hs = append(hs, rec.Hash)
				}
				snap[k] = hs
			}
		}
	}
	r, err := repo.Open(repoPath)
	must(err)
	fi, _ := os.Stat(repoPath)
	mi, _ := os.Stat(repoPath + ".m")
	whole := fi.Size() + mi.Size()
	for _, k := range behinds {
		held := map[archive.Hash]bool{}
		for _, h := range snap[k] {
			held[h] = true
		}
		var floor int64
		missing := 0
		counted := map[archive.Hash]bool{}
		for _, rec := range r.Arc.Records {
			if !held[rec.Hash] && !counted[rec.Hash] {
				counted[rec.Hash] = true
				missing++
				floor += int64(8 + len(rec.Data) + archive.HashLen)
			}
		}
		emit(Sample{Scenario: "s6", Commits: total, Behind: total - snapAt[k], WholeTransfer: whole, FloorTransfer: floor, MissingObjs: missing,
			HgsBytes: fi.Size(), MetaBytes: mi.Size(), Records: len(r.Arc.Records),
			Note: "floor = distinct records the replica lacks, before any manifest or compression"})
	}
}

// ---- report ----------------------------------------------------------------

// report renders one results file, or several (comma-separated) as a
// before/after comparison: the rows for the same measurement point, one per
// file in the order given, are printed next to each other.
func report(paths string) error {
	var rows []Sample
	for _, path := range strings.Split(paths, ",") {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if ln == "" {
				continue
			}
			var s Sample
			if err := json.Unmarshal([]byte(ln), &s); err != nil {
				return err
			}
			rows = append(rows, s)
		}
	}
	key := func(s Sample) string { return fmt.Sprint(s.Scenario, s.Note, s.Commits, s.Behind) }
	first := map[string]int{}
	for i, s := range rows {
		if _, ok := first[key(s)]; !ok {
			first[key(s)] = i
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return first[key(rows[i])] < first[key(rows[j])] })
	fmt.Println("| scen | variant | commits | .hgs bytes | records | unique | stored / floor | working set | open ms | offer ms | save ms | status ms | history ms | check ms | open alloc | open heap |")
	fmt.Println("|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, s := range rows {
		if s.Scenario == "s6" {
			continue
		}
		ratio := float64(s.HgsBytes) / float64(s.FloorBytes)
		fmt.Printf("| %s | %s | %d | %d | %d | %d | %.1fx | %d | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f | %d | %d |\n",
			s.Scenario, s.Variant, s.Commits, s.HgsBytes, s.Records, s.Unique, ratio, s.WorkBytes,
			s.OpenMs, s.OfferMs, s.SaveMs, s.StatusMs, s.HistoryMs, s.CheckMs, s.OpenAlloc, s.OpenHeap)
	}
	fmt.Println()
	fmt.Println("| variant | commits | replica behind | whole export/import bytes | records the replica lacks | floor bytes | whole / floor |")
	fmt.Println("|---|---:|---:|---:|---:|---:|---:|")
	for _, s := range rows {
		if s.Scenario == "s6" {
			fmt.Printf("| %s | %d | %d | %d | %d | %d | %.0fx |\n", s.Variant, s.Commits, s.Behind, s.WholeTransfer, s.MissingObjs, s.FloorTransfer,
				float64(s.WholeTransfer)/float64(s.FloorTransfer))
		}
	}
	return nil
}
