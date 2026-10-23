package json

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ottermq/ottermq/pkg/persistence"
)

// Names that would be dangerous as path components, plus names that only
// differ in case (case-insensitive filesystems) and names too long to fit
// in a file name once encoded.
var hostileNames = []string{
	"..", "../x", "../../escape", "a/b", `a\b`, ".", "", "unicodé-名前",
	"Case", "case", strings.Repeat("x", 255),
}

func newTestPersistence(t *testing.T, dataDir string) *JsonPersistence {
	t.Helper()
	p, err := NewJsonPersistence(&persistence.Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("NewJsonPersistence: %v", err)
	}
	return p
}

// filesUnder returns every regular file below root, relative to root.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return files
}

// Every file a save creates lands inside <data>/vhosts/<one component>/,
// whatever the vhost, queue and exchange names are.
func TestEntityFilesStayInsideTheirVHostDir(t *testing.T) {
	for _, vhost := range []string{"/", "..", "../..", "a/b", ""} {
		for _, name := range hostileNames {
			root := t.TempDir()
			data := filepath.Join(root, "data")
			p := newTestPersistence(t, data)

			if err := p.SaveVHostMetadata(vhost); err != nil {
				t.Fatalf("vhost %q: SaveVHostMetadata: %v", vhost, err)
			}
			if err := p.SaveQueueMetadata(vhost, name, persistence.QueueProperties{Durable: true}); err != nil {
				t.Fatalf("vhost %q queue %q: SaveQueueMetadata: %v", vhost, name, err)
			}
			if err := p.SaveMessage(vhost, name, "m1", []byte("x"), persistence.MessageProperties{DeliveryMode: 2}); err != nil {
				t.Fatalf("vhost %q queue %q: SaveMessage: %v", vhost, name, err)
			}
			if err := p.SaveExchangeMetadata(vhost, name, "direct", persistence.ExchangeProperties{Durable: true}); err != nil {
				t.Fatalf("vhost %q exchange %q: SaveExchangeMetadata: %v", vhost, name, err)
			}
			if err := p.SaveBindingState(vhost, name, name, "rk", nil); err != nil {
				t.Fatalf("vhost %q exchange %q: SaveBindingState: %v", vhost, name, err)
			}

			for _, file := range filesUnder(t, root) {
				parts := strings.Split(file, string(filepath.Separator))
				switch {
				case file == filepath.Join("data", layoutFileName):
				case len(parts) == 4 && parts[0] == "data" && parts[1] == "vhosts" && parts[3] == vhostNameFile:
				case len(parts) == 5 && parts[0] == "data" && parts[1] == "vhosts" &&
					(parts[3] == "queues" || parts[3] == "exchanges") && strings.HasSuffix(parts[4], ".json"):
				default:
					t.Errorf("vhost %q, name %q: file %s is outside <data>/vhosts/<vhost>/{queues,exchanges}/", vhost, name, file)
				}
			}
		}
	}
}

// Names persist and come back exactly, including names that are encoded
// with a hash because they are too long for a file name.
func TestEntityNamesRoundTrip(t *testing.T) {
	data := t.TempDir()
	p := newTestPersistence(t, data)
	vhosts := []string{"/", "..", "a/b", strings.Repeat("v", 255)}
	for _, vhost := range vhosts {
		if err := p.SaveVHostMetadata(vhost); err != nil {
			t.Fatalf("SaveVHostMetadata(%q): %v", vhost, err)
		}
		for _, name := range hostileNames {
			if err := p.SaveQueueMetadata(vhost, name, persistence.QueueProperties{Durable: true}); err != nil {
				t.Fatalf("SaveQueueMetadata(%q, %q): %v", vhost, name, err)
			}
			if err := p.SaveExchangeMetadata(vhost, name, "topic", persistence.ExchangeProperties{Durable: true}); err != nil {
				t.Fatalf("SaveExchangeMetadata(%q, %q): %v", vhost, name, err)
			}
		}
	}

	// A fresh instance on the same dir, as after a restart.
	p = newTestPersistence(t, data)
	gotVHosts, err := p.LoadAllVHosts()
	if err != nil {
		t.Fatalf("LoadAllVHosts: %v", err)
	}
	assertSameNames(t, "vhosts", gotVHosts, vhosts)

	for _, vhost := range vhosts {
		queues, err := p.LoadAllQueues(vhost)
		if err != nil {
			t.Fatalf("LoadAllQueues(%q): %v", vhost, err)
		}
		var queueNames []string
		for _, q := range queues {
			queueNames = append(queueNames, q.Name)
		}
		assertSameNames(t, "queues in "+vhost, queueNames, hostileNames)

		exchanges, err := p.LoadAllExchanges(vhost)
		if err != nil {
			t.Fatalf("LoadAllExchanges(%q): %v", vhost, err)
		}
		var exchangeNames []string
		for _, e := range exchanges {
			exchangeNames = append(exchangeNames, e.Name)
		}
		assertSameNames(t, "exchanges in "+vhost, exchangeNames, hostileNames)
	}
}

func assertSameNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// Deleting a vhost removes only that vhost's directory, whatever its name.
func TestDeleteVHostStaysInsideItsDir(t *testing.T) {
	for _, vhost := range []string{"..", ".", "", "../..", "a/b"} {
		data := t.TempDir()
		p := newTestPersistence(t, data)
		sentinel := filepath.Join(data, "ottermq.db")
		if err := os.WriteFile(sentinel, []byte("users"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := p.SaveQueueMetadata("other", "q", persistence.QueueProperties{Durable: true}); err != nil {
			t.Fatal(err)
		}
		if err := p.SaveQueueMetadata(vhost, "q", persistence.QueueProperties{Durable: true}); err != nil {
			t.Fatal(err)
		}

		if err := p.DeleteVHostMetadata(vhost); err != nil {
			t.Fatalf("DeleteVHostMetadata(%q): %v", vhost, err)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Errorf("DeleteVHostMetadata(%q) removed %s: %v", vhost, sentinel, err)
		}
		if queues, _ := p.LoadAllQueues("other"); len(queues) != 1 {
			t.Errorf("DeleteVHostMetadata(%q) removed another vhost's queue", vhost)
		}
		if queues, _ := p.LoadAllQueues(vhost); len(queues) != 0 {
			t.Errorf("DeleteVHostMetadata(%q) left %d queues behind", vhost, len(queues))
		}
	}
}

// A data dir written with the old layout (names used as path components) is
// refused with an error that says what to do, instead of being misread. That
// includes vhost directories with no files: the old directory of vhost "nco"
// would decode as vhost "f".
func TestOldLayoutIsRefused(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, data string){
		"queue file": func(t *testing.T, data string) {
			old := filepath.Join(data, "vhosts", "%2F", "queues")
			if err := os.MkdirAll(old, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "orders.json"), []byte(`{"name":"orders"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"empty vhost dirs": func(t *testing.T, data string) {
			for _, dir := range []string{"nco", "prod", "%2Fstaging"} {
				if err := os.MkdirAll(filepath.Join(data, "vhosts", dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			prepare(t, data)

			_, err := NewJsonPersistence(&persistence.Config{DataDir: data})
			if err == nil {
				t.Fatal("NewJsonPersistence accepted a data dir in the old layout")
			}
			for _, want := range []string{data, "layout"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			if _, err := os.Stat(filepath.Join(data, layoutFileName)); !os.IsNotExist(err) {
				t.Errorf("a refused data dir got a layout marker: %v", err)
			}
		})
	}
}

// A new data dir, and one whose vhosts/ directory is empty, get the current
// layout and open again after a restart.
func TestNewDataDirGetsCurrentLayout(t *testing.T) {
	for name, prepare := range map[string]func(string){
		"empty":            func(string) {},
		"empty vhosts dir": func(d string) { _ = os.MkdirAll(filepath.Join(d, "vhosts"), 0o755) },
	} {
		data := t.TempDir()
		prepare(data)
		newTestPersistence(t, data)
		if _, err := os.Stat(filepath.Join(data, layoutFileName)); err != nil {
			t.Errorf("%s: no layout marker after Initialize: %v", name, err)
		}
		newTestPersistence(t, data) // reopening is fine
	}
}
