package store

import (
	"log"
	"os"
	"path/filepath"
)

// writeFileAtomic implements docs/09-data-model.md's "Atomic write
// strategy": write to a temp file in tmpDir (same filesystem as dir, so the
// rename below is atomic), fsync it, rename over target, then fsync the
// containing directory so the rename itself is durable.
func writeFileAtomic(dir, tmpDir, target string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(tmpDir, filepath.Base(target)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: once the rename below succeeds, this is a no-op
	// (the file no longer exists at tmpName).
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}

	// The rename above already made data visible at target — as far as any
	// caller re-reading the file is concerned, the write is committed. A
	// failure to fsync the containing directory only risks the *rename
	// entry itself* not surviving a crash (i.e. a highly unlikely window
	// where a power loss right after this point could roll the directory
	// back to pre-rename state on some filesystems); it does not undo the
	// rename for any caller still running. Returning an error here would
	// make callers roll back their in-memory state while the file is
	// already committed on disk, causing memory/disk to diverge — worse
	// than the rare durability gap. So: log and treat the write as
	// successful.
	d, err := os.Open(dir)
	if err != nil {
		log.Printf("store: opening %s to fsync after rename: %v", dir, err)
		return nil
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		log.Printf("store: fsyncing directory %s after rename: %v", dir, err)
	}
	return nil
}
