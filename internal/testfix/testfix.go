// Package testfix locates the golden fixtures in the contract submodule.
// Tests only.
package testfix

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "contract", "fixtures")
}

// Path returns the absolute path of a fixture file.
func Path(name string) string { return filepath.Join(dir(), name) }

// Read returns a fixture's bytes.
func Read(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(Path(name))
	if err != nil {
		t.Fatalf("fixture %s: %v (is the contract submodule checked out?)", name, err)
	}
	return b
}

// Names lists fixture file names ending in suffix, sorted.
func Names(t testing.TB, suffix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir())
	if err != nil {
		t.Fatalf("fixtures dir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ExpectedLog returns expected.log with line endings normalised to \n.
func ExpectedLog(t testing.TB) string {
	t.Helper()
	return strings.ReplaceAll(string(Read(t, "expected.log")), "\r\n", "\n")
}
