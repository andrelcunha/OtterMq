package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
// first use.
func loadOrCreateJwtKey(dataDir string) (string, error) {
	path := filepath.Join(dataDir, jwtKeyFile)
	data, err := os.ReadFile(path)
	if err == nil {
		key := strings.TrimSpace(string(data))
		if len(key) < MinJwtSecretLen {
			return "", fmt.Errorf("%s holds a key shorter than %d bytes; remove it to have a new one generated", path, MinJwtSecretLen)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := hex.EncodeToString(raw)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadOrCreateJwtKey(dataDir) // created by a concurrent start
	}
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(key + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	return key, f.Close()
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
