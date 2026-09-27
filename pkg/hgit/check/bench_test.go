package check

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/VectorSophie/hgit-native/pkg/hgit/archive"
	"github.com/VectorSophie/hgit-native/pkg/hgit/object"
	"github.com/VectorSophie/hgit-native/pkg/hgit/repo"
)

// BenchmarkRun is check over a saved, reopened repository of 2000 2 KiB
// blobs in one tree: it resolves every object through Repo.Get.
func BenchmarkRun(b *testing.B) {
	p := filepath.Join(b.TempDir(), "r.hgs")
	if err := os.WriteFile(p, archive.Header{Version: 4}.Marshal(), 0o644); err != nil {
		b.Fatal(err)
	}
	r, err := repo.Open(p)
	if err != nil {
		b.Fatal(err)
	}
	t := &object.Tree{}
	for i := 0; i < 2000; i++ {
		h := r.Put(archive.Blob, bytes.Repeat([]byte(fmt.Sprint(i)), 2048/len(fmt.Sprint(i))))
		t.Entries = append(t.Entries, object.Entry{Name: fmt.Sprintf("f%04d", i), ChildType: archive.Blob, ChildHash: h, EntityID: uint64(i + 1)})
	}
	r.SetHead("main", commit(r, r.Put(archive.Tree, t.Encode())))
	if err := r.Save(); err != nil {
		b.Fatal(err)
	}
	if r, err = repo.Open(p); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Run(r)
	}
}
