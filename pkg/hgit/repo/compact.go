package repo

import (
	"errors"
	"fmt"
	"os"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
)

// ErrNothingToCompact is returned by Compact when the archive is already
// minimal: not torn, and holding no duplicate records. There is nothing
// honest to rewrite.
var ErrNothingToCompact = errors.New("repo: nothing to compact")

// Compact writes a new archive holding every distinct object in r exactly
// once, in first-occurrence order (ADR 0019 section 5) - the only way
// existing duplicate records (from a legacy/non-dedup archive) are removed,
// and how a torn archive gets an untorn replacement.
//
// Compact is explicitly NOT a garbage collector: every object currently in
// the archive survives, reachable or not, including dangling objects and
// anything an undo, a redo or an in-progress merge could still reach - only
// exact duplicate records of the same hash are dropped. If r was opened from
// a torn archive, r.Arc already holds only what ParseTolerant could read
// before the tear (see Open); Compact rewrites exactly that. That is
// expected, not a data loss - the torn bytes were never committed data to
// begin with.
//
// The original file is left untouched until the new one is fully written to
// a temporary file, then independently re-read and verified from disk - not
// from the bytes still in memory - by re-parsing strictly (Parse, so a fresh
// full rewrite must have no torn tail of its own) and recomputing every
// hash. Only then is it renamed over the original (the same atomic-replace
// discipline Save uses). Compact clears r's torn state; the caller can
// Save() again afterwards.
//
// Compact returns ErrNothingToCompact if there is nothing to remove and
// nothing torn to repair.
func (r *Repo) Compact() error {
	seen := make(map[archive.Hash]bool, len(r.Arc.Records))
	out := make([]archive.Record, 0, len(r.Arc.Records))
	for _, rec := range r.Arc.Records {
		if seen[rec.Hash] {
			continue
		}
		seen[rec.Hash] = true
		out = append(out, rec)
	}
	if !r.Torn() && len(out) == len(r.Arc.Records) {
		return ErrNothingToCompact
	}

	newArc := &archive.Archive{
		Header:  archive.Header{Version: r.Arc.Header.Version, Reserved: r.Arc.Header.Reserved, Count: uint64(len(out))},
		Records: out,
	}
	b := newArc.Marshal()

	tmp := r.Path + ".compact.tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	raw, err := os.ReadFile(tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	verify, err := archive.Parse(raw) // strict: a fresh rewrite must not itself be torn
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("repo: compact wrote an unreadable archive, not replacing the original: %w", err)
	}
	if total, ok := verify.Verify(); ok != total {
		os.Remove(tmp)
		return fmt.Errorf("repo: compact wrote %d corrupt record(s) out of %d, not replacing the original", total-ok, total)
	}
	if uint64(len(verify.Records)) != verify.Header.Count {
		os.Remove(tmp)
		return fmt.Errorf("repo: compact wrote a header count mismatch, not replacing the original")
	}

	if err := os.Rename(tmp, r.Path); err != nil {
		os.Remove(tmp)
		return err
	}

	r.Arc = newArc
	r.TornOffset, r.TornBytes = 0, 0
	r.idx = make(map[archive.Hash]int, len(out))
	for i, rec := range out {
		if _, dup := r.idx[rec.Hash]; !dup {
			r.idx[rec.Hash] = i
		}
	}
	return nil
}
