package bundle

import (
	"sort"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/clock"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// BuildOptions selects what Build sends. Have and Base are alternative
// prerequisite sources (BUNDLE.md "Building a bundle" step 2); if both are
// empty, prerequisites are none and the result is a baseline bundle.
type BuildOptions struct {
	Paths []string // nil = every path in r.PathList()
	Have  *HaveManifest
	Base  []archive.Hash
	Label string

	// CreatedMs overrides the manifest's creation time; 0 means clock.Now().
	// Tests use this for deterministic bundle bytes.
	CreatedMs uint64
}

type BuildResult struct {
	Data     []byte
	Kind     byte
	BundleID archive.Hash

	// FallbackReason is set when a given have-file's commits were all
	// unusable and Build fell back to a baseline bundle instead of silently
	// emitting a partial one (BUNDLE.md step 3).
	FallbackReason string
}

// Build implements BUNDLE.md's "Building a bundle".
func Build(r *repo.Repo, opts BuildOptions) (*BuildResult, error) {
	paths := opts.Paths
	if paths == nil {
		paths = r.PathList()
	}

	var heads []HeadEntry
	for _, p := range paths {
		h, ok := r.Head(p)
		if !ok {
			continue
		}
		if _, err := r.Commit(h); err != nil {
			// No commits on this path yet (e.g. a freshly-declared path
			// whose head is still all-zero) - nothing to propose for it.
			continue
		}
		heads = append(heads, HeadEntry{Name: p, Head: h})
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].Name < heads[j].Name })

	ancestorSets := make([]map[archive.Hash]bool, len(heads))
	for i, he := range heads {
		set, err := ancestorsOf(r.Commit, he.Head)
		if err != nil {
			return nil, err
		}
		ancestorSets[i] = set
	}
	isAncestorOfAHead := func(h archive.Hash) bool {
		for _, s := range ancestorSets {
			if s[h] {
				return true
			}
		}
		return false
	}

	var prereqs []archive.Hash
	fallback := ""
	switch {
	case opts.Have != nil:
		var candidates []archive.Hash
		for _, he := range opts.Have.Heads {
			candidates = append(candidates, he.Head)
		}
		candidates = append(candidates, opts.Have.Samples...)
		usable := map[archive.Hash]bool{}
		for _, c := range candidates {
			if usable[c] {
				continue
			}
			if _, err := r.Commit(c); err != nil {
				continue // not present in the sender's store, or not a commit
			}
			if isAncestorOfAHead(c) {
				usable[c] = true
				prereqs = append(prereqs, c)
			}
		}
		if len(prereqs) == 0 {
			fallback = ErrNoUsablePrereq.Error()
		}
	case len(opts.Base) > 0:
		for _, h := range opts.Base {
			if _, err := r.Commit(h); err == nil {
				prereqs = append(prereqs, h)
			}
		}
	}
	sort.Slice(prereqs, func(i, j int) bool { return prereqs[i].Hex() < prereqs[j].Hex() })

	prereqClosure := closure(r.Get, prereqs)
	objHashes := orderedClosure(r, heads, prereqClosure)

	var objRecords []archive.Record
	var objectBytes uint64
	for _, h := range objHashes {
		rec, ok := r.Get(h)
		if !ok {
			continue // unreachable: orderedClosure only walks resolvable hashes
		}
		objRecords = append(objRecords, rec)
		objectBytes += uint64(8 + len(rec.Data) + archive.HashLen)
	}

	kind := byte(KindBaseline)
	if len(prereqs) > 0 {
		kind = KindIncremental
	}
	createdMs := opts.CreatedMs
	if createdMs == 0 {
		createdMs = clock.Now()
	}

	m := Manifest{
		HashAlg:     HashAlgBLAKE2b512,
		Kind:        kind,
		CreatedMs:   createdMs,
		Label:       opts.Label,
		Prereqs:     prereqs,
		Heads:       heads,
		ObjectCount: uint32(len(objRecords)),
		ObjectBytes: objectBytes,
	}
	mc, err := m.Encode()
	if err != nil {
		return nil, err
	}
	manifestRec := archive.NewObject(TagManifest, mc)

	recs := append([]archive.Record{manifestRec}, objRecords...)
	body := marshalRecords(recs)
	hdr := marshalHeader("HGB0", header{Version: Version, Count: uint64(len(recs) + 1)})
	full := append(hdr, body...)

	bundleHash := archive.Sum(full)
	footerRec := archive.NewObject(TagFooter, footerContent(bundleHash))
	data := append(full, marshalRecords([]archive.Record{footerRec})...)

	return &BuildResult{Data: data, Kind: kind, BundleID: bundleHash, FallbackReason: fallback}, nil
}

// BuildHave builds a .hgh have-file: every open path's head plus, per head,
// up to k first-parent ancestors (k<=0 means DefaultHaveSamples),
// deduplicated (BUNDLE.md "Have-file").
func BuildHave(r *repo.Repo, k int) ([]byte, error) {
	if k <= 0 {
		k = DefaultHaveSamples
	}
	var heads []HeadEntry
	seen := map[archive.Hash]bool{}
	var samples []archive.Hash
	for _, p := range r.PathList() {
		h, ok := r.Head(p)
		if !ok {
			continue
		}
		if _, err := r.Commit(h); err != nil {
			continue
		}
		heads = append(heads, HeadEntry{Name: p, Head: h})
		cur := h
		for i := 0; i < k; i++ {
			c, err := r.Commit(cur)
			if err != nil || len(c.Parents) == 0 {
				break
			}
			cur = c.Parents[0]
			if !seen[cur] {
				seen[cur] = true
				samples = append(samples, cur)
			}
		}
	}

	hm := HaveManifest{Heads: heads, Samples: samples}
	hc, err := hm.Encode()
	if err != nil {
		return nil, err
	}
	haveRec := archive.NewObject(TagHaveManifest, hc)
	body := marshalRecords([]archive.Record{haveRec})
	hdr := marshalHeader("HGH0", header{Version: Version, Count: 2})
	full := append(hdr, body...)

	bundleHash := archive.Sum(full)
	footerRec := archive.NewObject(TagFooter, footerContent(bundleHash))
	return append(full, marshalRecords([]archive.Record{footerRec})...), nil
}

// ParseHave parses a .hgh file into its have-manifest.
func ParseHave(data []byte) (*HaveManifest, error) {
	h, err := parseHeader(data, "HGH0")
	if err != nil {
		return nil, err
	}
	recs, err := parseRecords(data, HeaderLen)
	if err != nil {
		return nil, err
	}
	if uint64(len(recs)) != h.Count || len(recs) != 2 {
		return nil, ErrCounts
	}
	if !recs[0].HashOK() || !recs[1].HashOK() {
		return nil, ErrHashMismatch
	}
	if recs[0].Type() != TagHaveManifest || recs[1].Type() != TagFooter {
		return nil, ErrLayout
	}
	fv, fh, err := decodeFooter(recs[1].Content())
	if err != nil {
		return nil, err
	}
	if fv != FooterVersion {
		return nil, ErrMalformed
	}
	beforeFooter := HeaderLen + len(marshalRecords(recs[:1]))
	if archive.Sum(data[:beforeFooter]) != fh {
		return nil, ErrFooterHash
	}
	return DecodeHaveManifest(recs[0].Content())
}
