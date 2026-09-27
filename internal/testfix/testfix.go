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

// legacyDir is the preserved pre-1.9 fixture set (ADR 0019): every object a
// writer touched got a new record, duplicates included. A test that forces
// legacy append mode (repo.SetDefaultDedup(false)) to reproduce that
// byte-for-byte reads fixtures from here, not from dir() - dir() is the
// current (1.9, store-once) golden set and its expected.log's object counts
// no longer match what a duplicate-appending writer produces.
func legacyDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "contract", "fixtures-1.8.9")
}

// Path returns the absolute path of a fixture file.
func Path(name string) string { return filepath.Join(dir(), name) }

// LegacyPath returns the absolute path of a pre-1.9 fixture file.
func LegacyPath(name string) string { return filepath.Join(legacyDir(), name) }

func read(t testing.TB, base, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(base, name))
	if err != nil {
		t.Fatalf("fixture %s: %v (is the contract submodule checked out?)", name, err)
	}
	return b
}

// Read returns a fixture's bytes.
func Read(t testing.TB, name string) []byte { t.Helper(); return read(t, dir(), name) }

// LegacyRead returns a pre-1.9 fixture's bytes.
func LegacyRead(t testing.TB, name string) []byte { t.Helper(); return read(t, legacyDir(), name) }

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

// LegacyExpectedLog returns the pre-1.9 expected.log, line endings normalised.
func LegacyExpectedLog(t testing.TB) string {
	t.Helper()
	return strings.ReplaceAll(string(LegacyRead(t, "expected.log")), "\r\n", "\n")
}
