package testfix

import (
	"strings"
	"testing"
)

func TestExpectedLogStartsAndHasNoCR(t *testing.T) {
	log := ExpectedLog(t)
	if !strings.HasPrefix(log, "TFULL_BEGIN") {
		t.Fatalf("unexpected start: %q", log[:20])
	}
	if strings.Contains(log, "\r") {
		t.Fatal("expected.log still contains CR")
	}
}

func TestNamesFindsElevenRepos(t *testing.T) {
	if got := len(Names(t, ".hgs")); got != 11 {
		t.Fatalf("want 11 .hgs fixtures, got %d", got)
	}
}
