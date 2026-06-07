// Package aferodav adapts a golang.org/x/net/webdav.FileSystem into an
// afero.Fs, so any WebDAV-backed filesystem can be used wherever afero is
// expected.
//
// Usage:
//
//	var wdfs webdav.FileSystem = myWebDAVBackend()
//	var afs  afero.Fs         = aferodav.New(wdfs)
//
//	// now use afs with any afero-aware library
//	data, _ := afero.ReadFile(afs, "/notes/hello.txt")
package aferodav

import (
	"context"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

// New wraps a webdav.FileSystem as an afero.Fs.
// The supplied context is used for every WebDAV call; pass context.Background()
// if you have no request-scoped context.
func New(fs webdav.FileSystem, ctx context.Context) afero.Fs {
	if ctx == nil {
		ctx = context.Background()
	}
	return &davFs{fs: fs, ctx: ctx}
}

// davFs is the afero.Fs implementation backed by a webdav.FileSystem.
type davFs struct {
	fs  webdav.FileSystem
	ctx context.Context
}

// Name implements afero.Fs.
func (d *davFs) Name() string { return "WebDAVFs" }

// Create implements afero.Fs (O_RDWR | O_CREATE | O_TRUNC, mode 0666).
func (d *davFs) Create(name string) (afero.File, error) {
	return d.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

// Mkdir implements afero.Fs.
func (d *davFs) Mkdir(name string, perm os.FileMode) error {
	return d.fs.Mkdir(d.ctx, name, perm)
}

// MkdirAll implements afero.Fs by walking the path and creating each missing
// segment. webdav.FileSystem has no MkdirAll, so we emulate it.
func (d *davFs) MkdirAll(p string, perm os.FileMode) error {
	p = path.Clean(p)
	// Collect segments to create from deepest to root, stop when one exists.
	missing := []string{}
	cur := p
	for cur != "/" && cur != "." {
		if _, err := d.fs.Stat(d.ctx, cur); err == nil {
			break // exists
		}
		missing = append(missing, cur)
		cur = path.Dir(cur)
	}
	// Create from shallowest to deepest.
	for i := len(missing) - 1; i >= 0; i-- {
		if err := d.fs.Mkdir(d.ctx, missing[i], perm); err != nil {
			return err
		}
	}
	return nil
}

// Open implements afero.Fs (read-only).
func (d *davFs) Open(name string) (afero.File, error) {
	return d.OpenFile(name, os.O_RDONLY, 0)
}

// OpenFile implements afero.Fs.
func (d *davFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	wf, err := d.fs.OpenFile(d.ctx, name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &davFile{wf: wf, name: name, fs: d.fs, ctx: d.ctx}, nil
}

// Remove implements afero.Fs. WebDAV RemoveAll on a file behaves like Remove.
func (d *davFs) Remove(name string) error {
	// Guard: do not remove a directory with Remove (mimic os.Remove behaviour).
	info, err := d.fs.Stat(d.ctx, name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &os.PathError{Op: "remove", Path: name, Err: os.ErrInvalid}
	}
	return d.fs.RemoveAll(d.ctx, name)
}

// RemoveAll implements afero.Fs.
func (d *davFs) RemoveAll(p string) error {
	return d.fs.RemoveAll(d.ctx, p)
}

// Rename implements afero.Fs.
func (d *davFs) Rename(oldname, newname string) error {
	return d.fs.Rename(d.ctx, oldname, newname)
}

// Stat implements afero.Fs.
func (d *davFs) Stat(name string) (os.FileInfo, error) {
	return d.fs.Stat(d.ctx, name)
}

// Chmod is a no-op: WebDAV does not expose chmod semantics.
func (d *davFs) Chmod(name string, mode os.FileMode) error { return nil }

// Chown is a no-op: WebDAV does not expose chown semantics.
func (d *davFs) Chown(name string, uid, gid int) error { return nil }

// Chtimes is a no-op for the same reason (WebDAV PROPPATCH could implement
// this but most backends don't support it reliably via the FileSystem API).
func (d *davFs) Chtimes(name string, atime time.Time, mtime time.Time) error { return nil }

// ── davFile ──────────────────────────────────────────────────────────────────

// davFile wraps a webdav.File to satisfy the fuller afero.File interface.
//
// webdav.File = http.File (Close/Read/Seek/Readdir/Stat) + io.Writer
//
// afero.File additionally requires:
//   - ReadAt([]byte, int64) (int, error)
//   - WriteAt([]byte, int64) (int, error)
//   - Readdirnames(n int) ([]string, error)
//   - Sync() error
//   - Truncate(size int64) error
//   - WriteString(s string) (int, error)
//   - Name() string
//
// ReadAt and WriteAt are emulated via Seek+Read/Write (not concurrency-safe;
// sufficient for sequential access patterns typical in afero usage).
// Truncate is emulated by reopening the file when required.
// Sync is a no-op (WebDAV has no fsync concept).
type davFile struct {
	wf   webdav.File
	name string
	fs   webdav.FileSystem
	ctx  context.Context
}

// ── afero.File pass-throughs ─────────────────────────────────────────────────

func (f *davFile) Close() error                                 { return f.wf.Close() }
func (f *davFile) Read(p []byte) (int, error)                   { return f.wf.Read(p) }
func (f *davFile) Seek(offset int64, whence int) (int64, error) { return f.wf.Seek(offset, whence) }
func (f *davFile) Write(p []byte) (int, error)                  { return f.wf.Write(p) }
func (f *davFile) Readdir(count int) ([]os.FileInfo, error)     { return f.wf.Readdir(count) }
func (f *davFile) Stat() (os.FileInfo, error)                   { return f.wf.Stat() }
func (f *davFile) Name() string                                 { return f.name }

// ── emulated methods ─────────────────────────────────────────────────────────

// ReadAt implements io.ReaderAt by seeking to offset, reading, then restoring
// the original position.
func (f *davFile) ReadAt(p []byte, off int64) (int, error) {
	cur, err := f.wf.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := f.wf.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, readErr := io.ReadFull(f.wf, p)
	// Restore position regardless of read error.
	if _, seekErr := f.wf.Seek(cur, io.SeekStart); seekErr != nil && readErr == nil {
		return n, seekErr
	}
	// io.ReadFull returns io.ErrUnexpectedEOF when fewer bytes than len(p) are
	// available. For ReadAt semantics we normalise this to io.EOF.
	if readErr == io.ErrUnexpectedEOF {
		readErr = io.EOF
	}
	return n, readErr
}

// WriteAt implements io.WriterAt by seeking to offset, writing, then restoring
// the original position.
func (f *davFile) WriteAt(p []byte, off int64) (int, error) {
	cur, err := f.wf.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := f.wf.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, writeErr := f.wf.Write(p)
	if _, seekErr := f.wf.Seek(cur, io.SeekStart); seekErr != nil && writeErr == nil {
		return n, seekErr
	}
	return n, writeErr
}

// Readdirnames implements afero.File by delegating to Readdir.
func (f *davFile) Readdirnames(n int) ([]string, error) {
	infos, err := f.wf.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, nil
}

// Truncate implements afero.File.
// WebDAV has no Truncate method, so we emulate it:
//   - grow: seek to end, write zeros
//   - shrink: not directly supported — we copy content, recreate the file, write back
func (f *davFile) Truncate(size int64) error {
	info, err := f.wf.Stat()
	if err != nil {
		return err
	}
	cur := info.Size()

	if size == cur {
		return nil
	}

	if size > cur {
		// Grow: append zero bytes.
		if _, err := f.wf.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		zeros := make([]byte, size-cur)
		_, err = f.wf.Write(zeros)
		return err
	}

	// Shrink: read first `size` bytes, reopen (truncate), write back.
	if _, err := f.wf.Seek(0, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(f.wf, buf); err != nil && err != io.ErrUnexpectedEOF {
		return err
	}

	newFile, err := f.fs.OpenFile(f.ctx, f.name, os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	_, writeErr := newFile.Write(buf)
	closeErr := newFile.Close()
	if writeErr != nil {
		return writeErr
	}

	// Reopen the underlying file for continued use.
	reopened, err := f.fs.OpenFile(f.ctx, f.name, os.O_RDWR, 0666)
	if err != nil {
		return err
	}
	_ = f.wf.Close()
	f.wf = reopened

	return closeErr
}

// Sync is a no-op: WebDAV has no fsync equivalent.
func (f *davFile) Sync() error { return nil }

// WriteString implements afero.File.
func (f *davFile) WriteString(s string) (int, error) {
	return f.wf.Write([]byte(s))
}

// ── path helpers ─────────────────────────────────────────────────────────────

// cleanPath normalises a path to absolute slash-separated form.
// Exported so callers building paths programmatically can use it.
func cleanPath(name string) string {
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return path.Clean(name)
}
