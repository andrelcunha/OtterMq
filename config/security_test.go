package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// With no secret configured, a key is generated once, kept in the data dir
// with mode 0600, and reused after a restart.
func TestResolveJwtSecret_GeneratesAndKeepsKey(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := &Config{DataDir: dataDir}

	first, weak, err := ResolveJwtSecret(cfg)
	if err != nil || weak {
		t.Fatalf("ResolveJwtSecret = %q, weak %v, %v", first, weak, err)
	}
	if len(first) < MinJwtSecretLen {
		t.Errorf("generated key has %d bytes, want at least %d", len(first), MinJwtSecretLen)
	}
	info, err := os.Stat(filepath.Join(dataDir, jwtKeyFile))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}

	again, _, err := ResolveJwtSecret(cfg)
	if err != nil || again != first {
		t.Errorf("second ResolveJwtSecret = %q, %v; want the same key %q", again, err, first)
	}
	if other, _, _ := ResolveJwtSecret(&Config{DataDir: t.TempDir()}); other == first {
		t.Error("two data dirs got the same generated key")
	}
}

// A configured secret is used as is when strong, and refused when it is the
// old default or short, unless OTTERMQ_INSECURE_DEV is set.
func TestResolveJwtSecret_ConfiguredSecret(t *testing.T) {
	strong := strings.Repeat("k", MinJwtSecretLen)
	tests := []struct {
		name        string
		secret      string
		insecureDev bool
		wantErr     bool
		wantWeak    bool
	}{
		{"strong", strong, false, false, false},
		{"old default", "secret", false, true, false},
		{"short", strong[:MinJwtSecretLen-1], false, true, false},
		{"old default, insecure dev", "secret", true, false, true},
		{"short, insecure dev", "short", true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			got, weak, err := ResolveJwtSecret(&Config{DataDir: dataDir, JwtSecret: tt.secret, InsecureDev: tt.insecureDev})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, want error %v", err, tt.wantErr)
			}
			if err == nil && (got != tt.secret || weak != tt.wantWeak) {
				t.Errorf("= %q, weak %v; want %q, weak %v", got, weak, tt.secret, tt.wantWeak)
			}
			if _, statErr := os.Stat(filepath.Join(dataDir, jwtKeyFile)); !os.IsNotExist(statErr) {
				t.Errorf("a configured secret still wrote %s", jwtKeyFile)
			}
		})
	}
}

// A truncated key file is an error rather than a weak key.
func TestResolveJwtSecret_ShortKeyFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, jwtKeyFile), []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveJwtSecret(&Config{DataDir: dataDir}); err == nil {
		t.Fatal("ResolveJwtSecret accepted a 3-byte key file")
	}
}

// Brokers starting at the same time on a fresh data dir all get the same key;
// none of them sees a half-written key file.
func TestResolveJwtSecret_ConcurrentCreation(t *testing.T) {
	const rounds, starts = 100, 24
	for round := 0; round < rounds; round++ {
		cfg := &Config{DataDir: filepath.Join(t.TempDir(), "data")}
		keys := make([]string, starts)
		errs := make([]error, starts)
		var ready, done sync.WaitGroup
		ready.Add(starts)
		done.Add(starts)
		for i := 0; i < starts; i++ {
			go func(i int) {
				defer done.Done()
				ready.Done()
				ready.Wait()
				keys[i], _, errs[i] = ResolveJwtSecret(cfg)
			}(i)
		}
		done.Wait()
		for i := range keys {
			if errs[i] != nil {
				t.Fatalf("round %d, start %d: %v", round, i, errs[i])
			}
			if keys[i] != keys[0] {
				t.Fatalf("round %d: start %d got key %q, start 0 got %q", round, i, keys[i], keys[0])
			}
		}
	}
}

// A temp file left behind by a start that crashed before publishing its key
// doesn't stop the next start, and the key is generated as usual.
func TestResolveJwtSecret_CrashLeftoverTempFile(t *testing.T) {
	dataDir := t.TempDir()
	leftover := filepath.Join(dataDir, jwtKeyFile+".tmp-123456")
	if err := os.WriteFile(leftover, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	key, _, err := ResolveJwtSecret(&Config{DataDir: dataDir})
	if err != nil {
		t.Fatalf("ResolveJwtSecret with a leftover temp file: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, jwtKeyFile))
	if err != nil || strings.TrimSpace(string(data)) != key {
		t.Fatalf("%s = %q, %v; want %q", jwtKeyFile, data, err, key)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name := e.Name(); name != jwtKeyFile && name != filepath.Base(leftover) {
			t.Errorf("unexpected file %s left in the data dir", name)
		}
	}
}

// OTTERMQ_LOOPBACK_USERS: unset means guest, empty means nobody, otherwise a
// comma-separated list.
func TestLoopbackUsersFromEnv(t *testing.T) {
	tests := []struct {
		name  string
		value *string
		want  []string
	}{
		{"unset", nil, []string{"guest"}},
		{"empty", ptr(""), nil},
		{"list", ptr(" guest, ops ,,monitor"), []string{"guest", "ops", "monitor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTTERMQ_LOOPBACK_USERS", "") // restored after the test
			if tt.value == nil {
				os.Unsetenv("OTTERMQ_LOOPBACK_USERS")
			} else {
				t.Setenv("OTTERMQ_LOOPBACK_USERS", *tt.value)
			}
			if got := loopbackUsersFromEnv(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loopbackUsersFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
