package json

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/ottermq/pkg/persistence"
)

func newQueueWithMessage(t *testing.T) (*JsonPersistence, string) {
	t.Helper()
	p, err := NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	if err := p.SaveQueueMetadata("/", "q", persistence.QueueProperties{Durable: true}); err != nil {
		t.Fatalf("SaveQueueMetadata: %v", err)
	}
	if err := p.SaveMessage("/", "q", "m1", []byte("x"), persistence.MessageProperties{DeliveryMode: 2}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	return p, p.queueFile("/", "q")
}

// Regression test for #238: a save must not rewrite the live file in place.
// A reader that opened the file before the save (or a crash part-way
// through it) must still see the complete previous version.
func TestSaveDoesNotRewriteLiveFileInPlace(t *testing.T) {
	p, file := newQueueWithMessage(t)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := p.SaveMessage("/", "q", "m2", []byte("y"), persistence.MessageProperties{DeliveryMode: 2}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	seen, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(seen) != string(before) {
		t.Fatalf("the open file changed under its reader: the save rewrote it in place\nbefore: %s\nafter:  %s", before, seen)
	}
	msgs, err := p.LoadMessages("/", "q")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("LoadMessages = %d messages, %v; want 2", len(msgs), err)
	}
}

// A crash between writing the temp file and renaming it over the live one
// leaves the live file intact.
func TestCrashBeforeRenameKeepsOldFile(t *testing.T) {
	p, file := newQueueWithMessage(t)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	// The process "dies" at the rename: the hook panics, so nothing after
	// it in the write path runs, as in a real crash.
	errCrash := errors.New("simulated crash before rename")
	renameFile = func(oldpath, newpath string) error { panic(errCrash) }
	t.Cleanup(func() { renameFile = os.Rename })

	var crashed any
	func() {
		defer func() { crashed = recover() }()
		_ = p.SaveMessage("/", "q", "m2", []byte("y"), persistence.MessageProperties{DeliveryMode: 2})
	}()
	if crashed != errCrash {
		t.Fatalf("SaveMessage did not reach the rename (recovered %v)", crashed)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("live file changed although the rename never happened\nbefore: %s\nafter:  %s", before, after)
	}

	// The crash left the temp file behind; the next start removes it and
	// recovers the old state.
	if _, err := os.Stat(file + ".tmp"); err != nil {
		t.Fatalf("expected the crash to leave %s.tmp: %v", filepath.Base(file), err)
	}
	renameFile = os.Rename
	p2, err := NewJsonPersistence(&persistence.Config{DataDir: p.dataDir})
	if err != nil {
		t.Fatalf("NewJsonPersistence after crash: %v", err)
	}
	if _, err := os.Stat(file + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file survived startup: %v", err)
	}
	msgs, err := p2.LoadMessages("/", "q")
	if err != nil || len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Fatalf("LoadMessages after crash = %+v, %v; want only m1", msgs, err)
	}
}

// Startup removes temp files left in queue and exchange directories, and
// leaves everything else alone.
func TestInitializeRemovesStrayTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, layoutFileName), []byte(`{"layout": 2}`), 0644); err != nil {
		t.Fatal(err)
	}
	vh := filepath.Join(dir, "vhosts", encodeName("/"))
	stray := []string{
		filepath.Join(vh, "queues", "q.json.tmp"),
		filepath.Join(vh, "exchanges", "ex.json.tmp"),
	}
	kept := []string{
		filepath.Join(vh, "queues", "q.json"),
		filepath.Join(dir, "other.tmp"),
	}
	for _, f := range append(append([]string{}, stray...), kept...) {
		if err := os.MkdirAll(filepath.Dir(f), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(`{"name":"q"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := NewJsonPersistence(&persistence.Config{DataDir: dir}); err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	for _, f := range stray {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("stray temp file %s was not removed (stat err %v)", f, err)
		}
	}
	for _, f := range kept {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s should have been kept: %v", f, err)
		}
	}
}

// Replacing a file keeps its permission bits, as os.WriteFile did.
func TestAtomicWriteKeepsFileMode(t *testing.T) {
	p, file := newQueueWithMessage(t)
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveMessage("/", "q", "m2", []byte("y"), persistence.MessageProperties{DeliveryMode: 2}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("file mode after save = %o, want 600", got)
	}
}

// mkdirAllDurable creates every missing directory.
func TestMkdirAllDurable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vhosts", "%2F", "queues")
	if err := mkdirAllDurable(dir, 0755); err != nil {
		t.Fatalf("mkdirAllDurable: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if err := mkdirAllDurable(dir, 0755); err != nil {
		t.Fatalf("mkdirAllDurable on an existing dir: %v", err)
	}
}

// A queue name whose file name fits but whose name.json.tmp would not can
// still be written.
func TestAtomicWriteLongName(t *testing.T) {
	p, err := NewJsonPersistence(&persistence.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	name := strings.Repeat("q", maxFileNameLen-len(".json"))
	if err := p.SaveQueueMetadata("/", name, persistence.QueueProperties{Durable: true}); err != nil {
		t.Fatalf("SaveQueueMetadata: %v", err)
	}
	if err := p.SaveMessage("/", name, "m1", []byte("x"), persistence.MessageProperties{DeliveryMode: 2}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	msgs, err := p.LoadMessages("/", name)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("LoadMessages = %d messages, %v; want 1", len(msgs), err)
	}
	if tmp := filepath.Base(tmpPath(p.queueFile("/", name))); len(tmp) > maxFileNameLen || !strings.HasSuffix(tmp, tmpSuffix) {
		t.Fatalf("temp name %q is too long or lacks %s", tmp, tmpSuffix)
	}
}
