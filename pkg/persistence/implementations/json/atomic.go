package json

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// tmpSuffix is appended to a file's name while it is being written.
const tmpSuffix = ".tmp"

// maxFileNameLen is the longest file name most filesystems accept (NAME_MAX).
const maxFileNameLen = 255

// renameFile is os.Rename; tests replace it to simulate a crash.
var renameFile = os.Rename

// tmpPath returns the temp file atomicWriteFile writes before renaming it to
// path: path+".tmp", or, when that name would be too long for the filesystem
// (entity names up to 250 bytes fit as name.json), a fixed short name derived
// from path. Both end in tmpSuffix, so startup cleanup finds them.
func tmpPath(path string) string {
	if len(filepath.Base(path))+len(tmpSuffix) <= maxFileNameLen {
		return path + tmpSuffix
	}
	sum := sha256.Sum256([]byte(path))
	return filepath.Join(filepath.Dir(path), hex.EncodeToString(sum[:16])+tmpSuffix)
}

// atomicWriteFile replaces path with data so that, after a crash at any
// point, path holds either its previous content or data, never a mix. It
// writes tmpPath(path) in the same directory, fsyncs and closes it, renames it
// over path and fsyncs the directory so the rename itself is durable. An
// existing file keeps its permission bits; perm applies to a new one.
//
// Callers must hold path's lock: the temp name is fixed, so two concurrent
// writers of one path would share it.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp := tmpPath(path)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := renameFile(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(path))
}

// mkdirMu makes mkdirAllDurable's check, create and sync one step, so a
// caller never sees a directory another caller created but hasn't synced yet.
var mkdirMu sync.Mutex

// mkdirAllDurable is os.MkdirAll that also fsyncs the parent of every
// directory it creates, so a new directory, and the files later written into
// it, survive a crash.
func mkdirAllDurable(dir string, perm os.FileMode) error {
	mkdirMu.Lock()
	defer mkdirMu.Unlock()
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, perm); err != nil {
		return err
	}
	for _, d := range missing {
		if err := syncDir(filepath.Dir(d)); err != nil {
			return err
		}
	}
	return nil
}

// syncDir fsyncs a directory so that entries created or renamed in it
// survive a crash. Windows cannot fsync a directory, and NTFS doesn't need it.
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

// removeStrayTempFiles deletes temp files that a crash in atomicWriteFile
// left in the queue and exchange directories under root. The live files next
// to them are intact, so the temp files are discarded.
func removeStrayTempFiles(root string) error {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), tmpSuffix) {
			return nil
		}
		switch filepath.Base(filepath.Dir(path)) {
		case "queues", "exchanges":
			return os.Remove(path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
