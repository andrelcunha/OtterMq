package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
