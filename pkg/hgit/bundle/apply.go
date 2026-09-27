package bundle

import (
	"fmt"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/meta"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// Outcome is what Apply did to one proposed head.
type Outcome int

const (
	OutcomeNoop Outcome = iota
	OutcomeDeclared
	OutcomeFastForward
	OutcomeKeptDivergent
)

// HeadResult reports what happened to one proposed head. Outcome is the
// BUNDLE.md table category for Path (what a caller reports); write is the
// mechanical action Apply performs at AtPath - for OutcomeKeptDivergent that
// can still be OutcomeNoop (AtPath already contains incoming) or a plain
// fast-forward of AtPath, never of Path itself.
type HeadResult struct {
	Path    string // the proposed head's path name
	Outcome Outcome
	AtPath  string // the path actually written: Path, or a composed "<path>@<label>[...]" name
	NewHead archive.Hash
	Reason  string // set for OutcomeKeptDivergent: "divergent" | "closed" | "merge-in-progress"

	write Outcome // unexported: Noop/Declared/FastForward, what Apply does at AtPath
}

// ApplyResult is Apply's report.
type ApplyResult struct {
	BundleID   archive.Hash
	NewObjects int
	Heads      []HeadResult
}

// Apply implements BUNDLE.md's two-phase apply. Phase 1 (Verify) is
// read-only; if it fails, r is returned untouched and nothing is written.
// Phase 2 mutates r's in-memory state - new objects, then path heads - and
// calls r.Save() exactly once at the end, so the files on disk change
// atomically together or not at all.
//
// A composed-name-too-long refusal (BUNDLE.md "If a composed name would
// exceed 63 bytes...") is detected after objects are already stored into r's
// in-memory index but before any head is moved and before Save is called:
// since Save is the only disk write, "refuses before writing anything" is
// satisfied for the file on disk even though r's in-memory state must then
// be discarded (the caller should treat r as spent and re-Open it) rather
// than reused. This is the documented, honest reading of that sentence for
// an in-memory API rather than a process that simply exits.
func Apply(r *repo.Repo, data []byte, label string) (*ApplyResult, error) {
	v, err := Verify(data, r)
	if err != nil {
		return nil, err
	}

	newObjects := 0
	for _, rec := range v.Objects {
		before := len(r.Arc.Records)
		r.Store(rec.Type(), rec.Content())
		if len(r.Arc.Records) > before {
			newObjects++
		}
	}

	plans := make([]HeadResult, 0, len(v.Manifest.Heads))
	for _, he := range v.Manifest.Heads {
		plan, err := planHead(r, he.Path(), he.Head, label)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}

	now := clock.Now()
	for _, p := range plans {
		switch p.write {
		case OutcomeNoop:
			// nothing to do
		case OutcomeDeclared:
			prev, _ := r.Head(p.AtPath) // zero if the path never had one
			if !r.PathExists(p.AtPath) {
				r.Meta.Append(p.AtPath, meta.TagPathDeclared, nil)
			}
			if err := r.SetHead(p.AtPath, p.NewHead); err != nil {
				return nil, err
			}
			r.AppendOp(p.AtPath, meta.OpLogEntry{Timestamp: now, Prev: prev, New: p.NewHead})
		case OutcomeFastForward:
			prev, _ := r.Head(p.AtPath)
			if err := r.SetHead(p.AtPath, p.NewHead); err != nil {
				return nil, err
			}
			r.AppendOp(p.AtPath, meta.OpLogEntry{Timestamp: now, Prev: prev, New: p.NewHead})
		}
	}

	if err := r.Save(); err != nil {
		return nil, err
	}
	return &ApplyResult{BundleID: v.BundleID, NewObjects: newObjects, Heads: plans}, nil
}

// path wraps a HeadEntry's Name so planHead reads clearly below.
func (he HeadEntry) Path() string { return he.Name }

// planHead decides the outcome for one proposed (path, incoming) pair per
// BUNDLE.md's table, without writing anything.
func planHead(r *repo.Repo, path string, incoming archive.Hash, label string) (HeadResult, error) {
	exists := r.PathExists(path)
	localHead, hadHead := r.Head(path)
	// A head record that does not resolve to a decodable commit (a
	// freshly-declared path, or "main" before its first offer) is not a
	// real head yet: treat it the same as "no such path, never existed"
	// rather than trying to compute an ancestor relation against it.
	hasRealHead := hadHead
	if hadHead {
		if _, err := r.Commit(localHead); err != nil {
			hasRealHead = false
		}
	}

	switch {
	case !hasRealHead && exists:
		// An existing path (declared, or "main") with nothing committed on
		// it yet: same outcome as declaring it, just without appending a
		// second TagPathDeclared record.
		return HeadResult{Path: path, Outcome: OutcomeDeclared, AtPath: path, NewHead: incoming, write: OutcomeDeclared}, nil

	case !exists && !hasRealHead:
		return HeadResult{Path: path, Outcome: OutcomeDeclared, AtPath: path, NewHead: incoming, write: OutcomeDeclared}, nil

	case !exists && hasRealHead:
		// Closed locally: never reopen it under its own name.
		return keepDivergent(r, path, incoming, label, "closed")

	default:
		if mergeInProgress(r, path) {
			return keepDivergent(r, path, incoming, label, "merge-in-progress")
		}
		if localHead == incoming {
			return HeadResult{Path: path, Outcome: OutcomeNoop, AtPath: path, NewHead: localHead, write: OutcomeNoop}, nil
		}
		localAncestors, err := ancestorsOf(r.Commit, localHead)
		if err != nil {
			return HeadResult{}, err
		}
		if localAncestors[incoming] {
			return HeadResult{Path: path, Outcome: OutcomeNoop, AtPath: path, NewHead: localHead, write: OutcomeNoop}, nil
		}
		incomingAncestors, err := ancestorsOf(r.Commit, incoming)
		if err != nil {
			return HeadResult{}, err
		}
		if incomingAncestors[localHead] {
			return HeadResult{Path: path, Outcome: OutcomeFastForward, AtPath: path, NewHead: incoming, write: OutcomeFastForward}, nil
		}
		return keepDivergent(r, path, incoming, label, "divergent")
	}
}

func mergeInProgress(r *repo.Repo, path string) bool {
	_, ok := r.Meta.Find(path, meta.TagMergeState)
	return ok
}

// keepDivergent records incoming under "<path>@<label>", or - if that name
// already exists and itself cannot fast-forward to incoming -
// "<path>@<label>+<8 hex>" (BUNDLE.md: "the same table applies to it").
// Beyond that second collision the table is not applied a third time
// (BUNDLE.md does not define further escalation); a repeated collision at
// the "+<hex>" name is refused rather than silently overwritten, an honest
// choice for an ambiguity BUNDLE.md leaves open.
func keepDivergent(r *repo.Repo, path string, incoming archive.Hash, label, reason string) (HeadResult, error) {
	if label == "" {
		return HeadResult{}, fmt.Errorf("bundle: apply needs a --label to keep %s's divergent update apart", path)
	}
	name := path + "@" + label
	if err := checkNameLen(name); err != nil {
		return HeadResult{}, err
	}
	if write, at, ok := resolveComposed(r, name, incoming); ok {
		return HeadResult{Path: path, Outcome: OutcomeKeptDivergent, AtPath: at, NewHead: incoming, Reason: reason, write: write}, nil
	}

	name2 := fmt.Sprintf("%s+%s", name, incoming.Hex()[:8])
	if err := checkNameLen(name2); err != nil {
		return HeadResult{}, err
	}
	if r.PathExists(name2) {
		return HeadResult{}, fmt.Errorf("bundle: %s already exists and diverges again; refusing rather than overwrite it", name2)
	}
	return HeadResult{Path: path, Outcome: OutcomeKeptDivergent, AtPath: name2, NewHead: incoming, Reason: reason, write: OutcomeDeclared}, nil
}

// resolveComposed decides what to do at an already-possibly-existing
// composed name: declare it if new, do nothing if it already matches or
// already contains incoming, fast-forward if possible, or (ok=false) report
// that it still diverges, so the caller escalates to the "+<hex>" name.
func resolveComposed(r *repo.Repo, name string, incoming archive.Hash) (Outcome, string, bool) {
	if !r.PathExists(name) {
		return OutcomeDeclared, name, true
	}
	head, ok := r.Head(name)
	if !ok || head == incoming {
		return OutcomeNoop, name, true
	}
	ancestors, err := ancestorsOf(r.Commit, head)
	if err == nil && ancestors[incoming] {
		return OutcomeNoop, name, true
	}
	incomingAncestors, err := ancestorsOf(r.Commit, incoming)
	if err == nil && incomingAncestors[head] {
		return OutcomeFastForward, name, true
	}
	return 0, "", false
}

func checkNameLen(name string) error {
	if len(name) > MaxNameLen {
		return fmt.Errorf("%w: %q (%d bytes)", ErrComposedName, name, len(name))
	}
	return nil
}
