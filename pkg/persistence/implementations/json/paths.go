package json

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// On-disk layout, version 2. Entity names come from clients and can be
// anything AMQP allows, so they never become path components as they are:
//
//	<data>/persistence.json                         {"layout": 2}
//	<data>/vhosts/<vhost>/vhost.json                the vhost's name
//	<data>/vhosts/<vhost>/queues/<queue>.json
//	<data>/vhosts/<vhost>/exchanges/<exchange>.json
//
// where each <name> is encodeName(name).
const (
	layoutVersion  = 2
	layoutFileName = "persistence.json"
	vhostNameFile  = "vhost.json"

	// maxEncodedLen keeps "<encoded>.json" and its temp file well under
	// maxFileNameLen.
	maxEncodedLen = 200
)

var nameEncoding = base32.HexEncoding.WithPadding(base32.NoPadding)

// encodeName turns an entity name into a single path component: "n" and the
// name in lowercase base32hex when that fits in maxEncodedLen, otherwise "h"
// and the hex SHA-256 of the name. Both use only lowercase letters and digits,
// so the result has no separator, is never "." or "..", and two names can't
// collide on a case-insensitive filesystem.
func encodeName(name string) string {
	enc := strings.ToLower(nameEncoding.EncodeToString([]byte(name)))
	if 1+len(enc) <= maxEncodedLen {
		return "n" + enc
	}
	sum := sha256.Sum256([]byte(name))
	return "h" + hex.EncodeToString(sum[:])
}

// decodeName reverses encodeName. ok is false for a hashed component, whose
// name is read from the file it names instead, and for anything encodeName
// can't have produced.
func decodeName(component string) (name string, ok bool) {
	enc, found := strings.CutPrefix(component, "n")
	if !found {
		return "", false
	}
	b, err := nameEncoding.DecodeString(strings.ToUpper(enc))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (jp *JsonPersistence) vhostsDir() string {
	return filepath.Join(jp.dataDir, "vhosts")
}

func (jp *JsonPersistence) vhostDir(vhost string) string {
	return filepath.Join(jp.vhostsDir(), encodeName(vhost))
}

func (jp *JsonPersistence) queuesDir(vhost string) string {
	return filepath.Join(jp.vhostDir(vhost), "queues")
}

func (jp *JsonPersistence) exchangesDir(vhost string) string {
	return filepath.Join(jp.vhostDir(vhost), "exchanges")
}

// checkVHostDir makes sure dir is a direct child of the vhosts directory
// before something removes it. encodeName already guarantees this; the check
// keeps a future bug in the encoding from deleting anything else.
func (jp *JsonPersistence) checkVHostDir(dir string) error {
	rel, err := filepath.Rel(jp.vhostsDir(), dir)
	if err != nil || !filepath.IsLocal(rel) || strings.ContainsRune(rel, filepath.Separator) {
		return fmt.Errorf("refusing to remove %s: not a vhost directory under %s", dir, jp.vhostsDir())
	}
	return nil
}

type vhostNameData struct {
	Name string `json:"name"`
}

// ensureVHostDir creates the vhost's directory and writes vhost.json, which
// keeps the vhost's name when its directory name is a hash.
func (jp *JsonPersistence) ensureVHostDir(vhost string) error {
	dir := jp.vhostDir(vhost)
	file := filepath.Join(dir, vhostNameFile)
	if _, err := os.Stat(file); err == nil {
		return nil
	}
	if err := mkdirAllDurable(dir, 0755); err != nil {
		return err
	}
	data, err := json.Marshal(vhostNameData{Name: vhost})
	if err != nil {
		return err
	}
	defer jp.lockFile(file)()
	return atomicWriteFile(file, data, 0644)
}

// vhostName returns the name of the vhost stored in dir (a component of the
// vhosts directory), or ok=false if dir is not a vhost directory.
func (jp *JsonPersistence) vhostName(dir string) (string, bool) {
	if name, ok := decodeName(dir); ok {
		return name, true
	}
	data, err := os.ReadFile(filepath.Join(jp.vhostsDir(), dir, vhostNameFile))
	if err != nil {
		return "", false
	}
	var v vhostNameData
	if err := json.Unmarshal(data, &v); err != nil || encodeName(v.Name) != dir {
		return "", false
	}
	return v.Name, true
}

// entityName returns the name of the queue or exchange stored in file, a
// "<component>.json" entry, given the name recorded inside the file.
func entityName(file, recorded string) (string, bool) {
	component := strings.TrimSuffix(file, ".json")
	if name, ok := decodeName(component); ok {
		return name, true
	}
	if strings.HasPrefix(component, "h") && encodeName(recorded) == component {
		return recorded, true
	}
	return "", false
}

type layoutData struct {
	Layout int `json:"layout"`
}

// checkLayout makes sure the data dir uses the current layout. A data dir with
// no marker and nothing in vhosts/ is new and gets one. One with anything in
// vhosts/ but no marker was written by an older version, which used entity
// names as file paths; it is refused rather than misread. That includes
// vhost directories with no files: their old names can decode as other vhosts.
func (jp *JsonPersistence) checkLayout() error {
	marker := filepath.Join(jp.dataDir, layoutFileName)
	data, err := os.ReadFile(marker)
	if err == nil {
		var l layoutData
		if err := json.Unmarshal(data, &l); err != nil {
			return fmt.Errorf("read %s: %w", marker, err)
		}
		if l.Layout != layoutVersion {
			return fmt.Errorf("data dir %s uses persistence layout %d, but this version only reads layout %d", jp.dataDir, l.Layout, layoutVersion)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}

	old, err := hasEntries(jp.vhostsDir())
	if err != nil {
		return err
	}
	if old {
		return fmt.Errorf("data dir %s was written by OtterMQ v0.21 or earlier, whose persistence layout used queue, exchange and vhost names as file paths; "+
			"this version can't read that layout. Back up and remove %s to start with an empty store (vhosts, durable queues, exchanges and persistent messages in it are lost), "+
			"or keep running the previous version. See the upgrade notes in CHANGELOG.md", jp.dataDir, jp.vhostsDir())
	}

	data, err = json.Marshal(layoutData{Layout: layoutVersion})
	if err != nil {
		return err
	}
	return atomicWriteFile(marker, data, 0644)
}

// hasEntries reports whether dir exists and is not empty.
func hasEntries(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	return len(entries) > 0, err
}
