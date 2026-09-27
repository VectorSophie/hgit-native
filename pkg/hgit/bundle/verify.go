package bundle

import (
	"fmt"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// Verified is the result of a bundle's structural verify (BUNDLE.md Phase 1,
// steps 1-3): everything a bundle's own bytes can prove, with no repo
// needed. VerifyAgainstRepo (steps 4-5) needs a target repository and is
// kept separate so a caller can report a structural failure without ever
// opening one.
type Verified struct {
	Manifest Manifest
	Objects  []archive.Record // records 1..N-2, in bundle order
	byHash   map[archive.Hash]archive.Record
	BundleID archive.Hash
}

func hashList(hs []archive.Hash) string {
	parts := make([]string, len(hs))
	for i, h := range hs {
		parts[i] = h.Hex()[:16]
	}
	return strings.Join(parts, " ")
}

// MissingPrereqError is returned by VerifyAgainstRepo when the recipient
// lacks one or more prerequisite commits (BUNDLE.md Phase 1 step 4).
type MissingPrereqError struct{ Missing []archive.Hash }

func (e *MissingPrereqError) Error() string {
	return fmt.Sprintf("bundle: missing prerequisite commit(s), a baseline bundle is needed: %s", hashList(e.Missing))
}
func (e *MissingPrereqError) Unwrap() error { return ErrMissingPrereq }

// UnresolvedRefError is returned when an object's reference (or a proposed
// head) does not resolve inside the bundle or the recipient (Phase 1 step 5).
type UnresolvedRefError struct{ Missing []archive.Hash }

func (e *UnresolvedRefError) Error() string {
	return fmt.Sprintf("bundle: unresolved reference(s): %s", hashList(e.Missing))
}
func (e *UnresolvedRefError) Unwrap() error { return ErrUnresolvedRef }

// StructuralVerify checks a bundle's bytes alone: header, record framing and
// hashes, manifest/footer position, legal type tags, required_caps/hash_alg,
// record/object counts and the footer hash. It never touches a repository
// and never allocates from an untrusted length before bounds-checking it.
func StructuralVerify(data []byte) (*Verified, error) {
	hdr, err := parseHeader(data, "HGB0")
	if err != nil {
		return nil, err
	}
	recs, err := parseRecords(data, HeaderLen)
	if err != nil {
		return nil, err
	}
	if uint64(len(recs)) != hdr.Count {
		return nil, ErrCounts
	}
	if len(recs) < 2 {
		return nil, ErrLayout
	}
	for _, r := range recs {
		if !r.HashOK() {
			return nil, ErrHashMismatch
		}
	}
	if recs[0].Type() != TagManifest {
		return nil, ErrLayout
	}
	if recs[len(recs)-1].Type() != TagFooter {
		return nil, ErrLayout
	}
	middle := recs[1 : len(recs)-1]
	for _, r := range middle {
		switch r.Type() {
		case archive.Blob, archive.Tree, archive.Commit, archive.Attrs, archive.Conflict:
		default:
			return nil, ErrObjectType
		}
	}

	m, err := DecodeManifest(recs[0].Content())
	if err != nil {
		return nil, err
	}
	if m.RequiredCaps != 0 {
		return nil, ErrRequiredCaps
	}
	if m.HashAlg != HashAlgBLAKE2b512 {
		return nil, ErrHashAlg
	}

	var objectBytes uint64
	for _, r := range middle {
		content := r.Content()
		var derr error
		switch r.Type() {
		case archive.Tree:
			_, derr = object.DecodeTree(content)
		case archive.Commit:
			_, derr = object.DecodeCommit(content)
		case archive.Attrs:
			_, derr = object.DecodeAttrs(content)
		case archive.Conflict:
			_, derr = object.DecodeConflict(content)
			// archive.Blob: opaque bytes, nothing to decode.
		}
		if derr != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, derr)
		}
		objectBytes += uint64(8 + len(r.Data) + archive.HashLen)
	}
	if uint64(len(middle)) != uint64(m.ObjectCount) {
		return nil, ErrCounts
	}
	if objectBytes != m.ObjectBytes {
		return nil, ErrCounts
	}

	fv, fh, err := decodeFooter(recs[len(recs)-1].Content())
	if err != nil {
		return nil, err
	}
	if fv != FooterVersion {
		return nil, fmt.Errorf("%w: footer_version %d", ErrMalformed, fv)
	}
	beforeFooter := HeaderLen + len(marshalRecords(recs[:len(recs)-1]))
	bundleHash := archive.Sum(data[:beforeFooter])
	if bundleHash != fh {
		return nil, ErrFooterHash
	}

	byHash := make(map[archive.Hash]archive.Record, len(middle))
	for _, r := range middle {
		if _, dup := byHash[r.Hash]; !dup {
			byHash[r.Hash] = r
		}
	}

	return &Verified{Manifest: *m, Objects: append([]archive.Record(nil), middle...), byHash: byHash, BundleID: bundleHash}, nil
}

// get resolves h from the bundle first, then the repo - "inside the bundle
// or the recipient" (BUNDLE.md Phase 1 step 5).
func (v *Verified) get(r *repo.Repo, h archive.Hash) (archive.Record, bool) {
	if rec, ok := v.byHash[h]; ok {
		return rec, true
	}
	return r.Get(h)
}

// VerifyAgainstRepo checks Phase 1 steps 4-5 against r: every prerequisite
// present, every object reference and every proposed head resolvable inside
// the bundle or r. It performs no writes.
func (v *Verified) VerifyAgainstRepo(r *repo.Repo) error {
	var missingPrereq []archive.Hash
	for _, p := range v.Manifest.Prereqs {
		if _, ok := r.Get(p); !ok {
			missingPrereq = append(missingPrereq, p)
		}
	}
	if len(missingPrereq) > 0 {
		return &MissingPrereqError{Missing: missingPrereq}
	}

	var unresolved []archive.Hash
	seenMissing := map[archive.Hash]bool{}
	note := func(h archive.Hash) {
		if !seenMissing[h] {
			seenMissing[h] = true
			unresolved = append(unresolved, h)
		}
	}
	for _, rec := range v.Objects {
		refs, ok := objectRefs(rec.Type(), rec.Content())
		if !ok {
			continue // already refused in StructuralVerify's strict decode
		}
		for _, h := range refs {
			if _, ok := v.get(r, h); !ok {
				note(h)
			}
		}
	}
	for _, he := range v.Manifest.Heads {
		rec, ok := v.get(r, he.Head)
		if !ok || rec.Type() != archive.Commit {
			note(he.Head)
			continue
		}
		if _, err := object.DecodeCommit(rec.Content()); err != nil {
			note(he.Head)
		}
	}
	if len(unresolved) > 0 {
		return &UnresolvedRefError{Missing: unresolved}
	}
	return nil
}

// Verify runs both phases: StructuralVerify, then VerifyAgainstRepo. It
// performs no writes; a failure here leaves r untouched.
func Verify(data []byte, r *repo.Repo) (*Verified, error) {
	v, err := StructuralVerify(data)
	if err != nil {
		return nil, err
	}
	if err := v.VerifyAgainstRepo(r); err != nil {
		return nil, err
	}
	return v, nil
}
