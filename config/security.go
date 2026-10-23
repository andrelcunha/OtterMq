package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// MinJwtSecretLen is the shortest OTTERMQ_JWT_SECRET the broker accepts.
const MinJwtSecretLen = 32

// jwtKeyFile holds the generated key in the data dir when no
// OTTERMQ_JWT_SECRET is configured.
const jwtKeyFile = "jwt.key"

// ResolveJwtSecret returns the key that signs web API tokens. With
// OTTERMQ_JWT_SECRET unset, a random key is generated on first start and kept
// in <data dir>/jwt.key (mode 0600), so tokens survive restarts. A configured
// key that is the old default "secret" or shorter than MinJwtSecretLen is
// refused, unless InsecureDev is set; weak then reports it so the caller can
// warn.
func ResolveJwtSecret(cfg *Config) (secret string, weak bool, err error) {
	if cfg.JwtSecret == "" {
		secret, err := loadOrCreateJwtKey(cfg.DataDir)
		return secret, false, err
	}
	if cfg.JwtSecret != "secret" && len(cfg.JwtSecret) >= MinJwtSecretLen {
		return cfg.JwtSecret, false, nil
	}
	if !cfg.InsecureDev {
		return "", false, fmt.Errorf("OTTERMQ_JWT_SECRET is too weak (the old default \"secret\", or shorter than %d bytes): "+
			"set a random value of at least %d bytes, or unset it to have one generated in the data dir", MinJwtSecretLen, MinJwtSecretLen)
	}
	return cfg.JwtSecret, true, nil
}

// loadOrCreateJwtKey reads the key from <dataDir>/jwt.key, generating it on
// first use. A new key is written to a temp file and linked into place, so
// jwt.key either doesn't exist or holds a whole key, even if the broker
// crashes or several brokers start on the same data dir at once.
func loadOrCreateJwtKey(dataDir string) (string, error) {
	path := filepath.Join(dataDir, jwtKeyFile)
	key, err := readJwtKey(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return key, err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key = hex.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dataDir, jwtKeyFile+".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(key + "\n"); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return readJwtKey(path) // created by a concurrent start
		}
		return "", err
	}
	return key, syncDir(dataDir)
}

// readJwtKey reads and checks a key file written by loadOrCreateJwtKey.
func readJwtKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if len(key) < MinJwtSecretLen {
		return "", fmt.Errorf("%s holds a key shorter than %d bytes; remove it to have a new one generated", path, MinJwtSecretLen)
	}
	return key, nil
}

// syncDir flushes a directory entry, so a new link survives a crash.
// Windows cannot fsync a directory, and NTFS doesn't need it.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

// loopbackUsersFromEnv reads OTTERMQ_LOOPBACK_USERS, a comma-separated list
// of users that may only connect from a loopback address. Unset means
// "guest"; set to an empty value, no user is restricted.
func loopbackUsersFromEnv() []string {
	value, ok := os.LookupEnv("OTTERMQ_LOOPBACK_USERS")
	if !ok {
		return []string{"guest"}
	}
	var users []string
	for _, user := range strings.Split(value, ",") {
		if user = strings.TrimSpace(user); user != "" {
			users = append(users, user)
		}
	}
	return users
}
