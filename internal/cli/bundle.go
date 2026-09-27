package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/bundle"
)

// splitFlags is bundle create/apply's own tiny argument parser: it accepts
// "--name value" pairs interspersed anywhere among positional arguments
// (BUNDLE.md's CLI shape puts them after the positional repo/bundle paths,
// which Go's flag package cannot parse - it stops at the first non-flag
// argument). Repeated flags (--base, --paths) accumulate.
func splitFlags(a []string) (pos []string, vals map[string][]string, err error) {
	vals = map[string][]string{}
	for i := 0; i < len(a); i++ {
		s := a[i]
		if !strings.HasPrefix(s, "--") {
			pos = append(pos, s)
			continue
		}
		name := s[2:]
		if i+1 >= len(a) {
			return nil, nil, fmt.Errorf("--%s needs a value", name)
		}
		i++
		vals[name] = append(vals[name], a[i])
	}
	return pos, vals, nil
}

func firstOr(vs []string, def string) string {
	if len(vs) > 0 {
		return vs[0]
	}
	return def
}

func runBundleCreate(c *ctx, a []string) int {
	usage := "bundle create <repo_path> <out.hgb> [--have <have.hgh>] [--base <hash>]... [--paths <name>]... [--label <name>]"
	pos, flags, err := splitFlags(a)
	if err != nil {
		return c.usageErr("", "bundle create: "+err.Error(), usage)
	}
	if len(pos) != 2 {
		return c.usageErr("", "bundle create: wrong number of arguments", usage)
	}
	repoPath, outPath := pos[0], pos[1]

	r, ok := c.open("BUNDLE", repoPath)
	if !ok {
		return ExitFail
	}

	opts := bundle.BuildOptions{Label: firstOr(flags["label"], "")}
	if hp := flags["have"]; len(hp) > 0 {
		hb, err := os.ReadFile(hp[0])
		if err != nil {
			return c.fail("", fmt.Sprintf("bundle create: cannot read have-file: %v", err))
		}
		hm, err := bundle.ParseHave(hb)
		if err != nil {
			return c.fail("", fmt.Sprintf("bundle create: bad have-file: %v", err))
		}
		opts.Have = hm
	}
	for _, hx := range flags["base"] {
		h, err := archive.ParseHex(hx)
		if err != nil {
			return c.fail("", fmt.Sprintf("bundle create: bad --base hash %q: %v", hx, err))
		}
		opts.Base = append(opts.Base, h)
	}
	if p := flags["paths"]; len(p) > 0 {
		opts.Paths = p
	}

	res, err := bundle.Build(r, opts)
	if err != nil {
		return c.fail("", fmt.Sprintf("bundle create: %v", err))
	}
	if err := os.WriteFile(outPath, res.Data, 0o644); err != nil {
		return c.fail("", fmt.Sprintf("bundle create: %v", err))
	}

	kindWord := "baseline"
	if res.Kind == bundle.KindIncremental {
		kindWord = "incremental"
	}
	if c.serial {
		fmt.Fprintf(c.out, "BUNDLE_OK create %s kind=%s id=%s bytes=%d\n", outPath, kindWord, res.BundleID.Hex()[:16], len(res.Data))
		return ExitOK
	}
	fmt.Fprintf(c.out, "Wrote %s bundle %s (%d bytes, id %s).\n", kindWord, outPath, len(res.Data), res.BundleID.Hex()[:16])
	if res.FallbackReason != "" {
		fmt.Fprintf(c.out, "Note: %s\n", res.FallbackReason)
	}
	return ExitOK
}

func runBundleInspect(c *ctx, a []string) int {
	data, err := os.ReadFile(a[0])
	if err != nil {
		return c.fail("BUNDLE_ERR "+a[0]+"\n", fmt.Sprintf("bundle inspect: %v", err))
	}
	v, err := bundle.StructuralVerify(data)
	if err != nil {
		return c.fail("BUNDLE_ERR bad_bundle\n", fmt.Sprintf("bundle inspect: %v", err))
	}
	kindWord := "baseline"
	if v.Manifest.Kind == bundle.KindIncremental {
		kindWord = "incremental"
	}
	if c.serial {
		fmt.Fprintf(c.out, "BUNDLE_INFO kind=%s label=%q id=%s prereqs=%d heads=%d objects=%d bytes=%d\n",
			kindWord, v.Manifest.Label, v.BundleID.Hex()[:16], len(v.Manifest.Prereqs), len(v.Manifest.Heads),
			v.Manifest.ObjectCount, v.Manifest.ObjectBytes)
		for _, he := range v.Manifest.Heads {
			fmt.Fprintf(c.out, "BUNDLE_HEAD %s %s\n", he.Name, he.Head.Hex())
		}
		return ExitOK
	}
	fmt.Fprintf(c.out, "kind: %s\nlabel: %q\nbundle id: %s\n", kindWord, v.Manifest.Label, v.BundleID.Hex()[:16])
	fmt.Fprintf(c.out, "prerequisites: %d\n", len(v.Manifest.Prereqs))
	fmt.Fprintf(c.out, "heads: %d\n", len(v.Manifest.Heads))
	for _, he := range v.Manifest.Heads {
		fmt.Fprintf(c.out, "  %s -> %s\n", he.Name, he.Head.Hex()[:16])
	}
	fmt.Fprintf(c.out, "objects: %d (%d bytes)\n", v.Manifest.ObjectCount, v.Manifest.ObjectBytes)
	return ExitOK
}

func runBundleApply(c *ctx, a []string) int {
	usage := "bundle apply <repo_path> <bundle.hgb> [--label <name>]"
	pos, flags, err := splitFlags(a)
	if err != nil {
		return c.usageErr("", "bundle apply: "+err.Error(), usage)
	}
	if len(pos) != 2 {
		return c.usageErr("", "bundle apply: wrong number of arguments", usage)
	}
	repoPath, bundlePath := pos[0], pos[1]
	label := firstOr(flags["label"], "")

	r, ok := c.open("BUNDLE", repoPath)
	if !ok {
		return ExitFail
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return c.fail("BUNDLE_ERR "+bundlePath+"\n", fmt.Sprintf("bundle apply: %v", err))
	}
	ar, err := bundle.Apply(r, data, label)
	if err != nil {
		return c.fail("BUNDLE_ERR apply_failed\n", fmt.Sprintf("bundle apply: %v", err))
	}

	if c.serial {
		fmt.Fprintf(c.out, "BUNDLE_OK apply id=%s new_objects=%d\n", ar.BundleID.Hex()[:16], ar.NewObjects)
		for _, h := range ar.Heads {
			fmt.Fprintf(c.out, "BUNDLE_HEAD_RESULT %s at=%s outcome=%s reason=%s\n", h.Path, h.AtPath, outcomeWord(h.Outcome), h.Reason)
		}
		return ExitOK
	}
	fmt.Fprintf(c.out, "Applied bundle %s: %d new object(s).\n", bundlePath, ar.NewObjects)
	for _, h := range ar.Heads {
		switch h.Outcome {
		case bundle.OutcomeNoop:
			fmt.Fprintf(c.out, "  %s: already up to date.\n", h.Path)
		case bundle.OutcomeDeclared:
			fmt.Fprintf(c.out, "  %s: declared at %s.\n", h.Path, h.NewHead.Hex()[:16])
		case bundle.OutcomeFastForward:
			fmt.Fprintf(c.out, "  %s: fast-forwarded to %s.\n", h.Path, h.NewHead.Hex()[:16])
		case bundle.OutcomeKeptDivergent:
			fmt.Fprintf(c.out, "  %s: kept local head; incoming (%s) recorded under %s.\n", h.Path, h.Reason, h.AtPath)
		}
	}
	return ExitOK
}

func outcomeWord(o bundle.Outcome) string {
	switch o {
	case bundle.OutcomeNoop:
		return "noop"
	case bundle.OutcomeDeclared:
		return "declared"
	case bundle.OutcomeFastForward:
		return "fast-forward"
	case bundle.OutcomeKeptDivergent:
		return "kept-divergent"
	default:
		return "unknown"
	}
}

func runHave(c *ctx, a []string) int {
	r, ok := c.open("HAVE", a[0])
	if !ok {
		return ExitFail
	}
	data, err := bundle.BuildHave(r, 0)
	if err != nil {
		return c.fail("HAVE_ERR build_failed\n", fmt.Sprintf("have: %v", err))
	}
	if err := os.WriteFile(a[1], data, 0o644); err != nil {
		return c.fail("HAVE_ERR "+a[1]+"\n", fmt.Sprintf("have: %v", err))
	}
	c.say(fmt.Sprintf("HAVE_OK %s\n", a[1]), fmt.Sprintf("Wrote have-file %s (%d bytes).\n", a[1], len(data)))
	return ExitOK
}
