//go:build windows

package filestore

// syncDir has nothing to flush on Windows, and saying so out loud is the point
// of the file.
//
// There is no directory handle to hand FlushFileBuffers: a directory opened
// with FILE_FLAG_BACKUP_SEMANTICS refuses it, and Go's os.Rename is MoveFileEx,
// which NTFS records in its metadata journal. The durability of the entry is
// therefore the filesystem's to keep here, and the file's own Sync before the
// rename is what this code is responsible for.
//
// A no-op with a reason beats a build tag on the call site: the caller reads
// the same on all three platforms, and the platform difference is written down
// where somebody porting this will look for it.
func syncDir(string) error { return nil }
