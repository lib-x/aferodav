package aferodav

import (
	"context"
	"os"
	"path"

	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

// NewFS wraps an afero.Fs as a webdav.FileSystem, so it can be used directly
// as the FileSystem field of a webdav.Handler.
//
// Usage:
//
//	afs := afero.NewMemMapFs()
//	handler := &webdav.Handler{
//	    FileSystem: aferodav.NewFS(afs),
//	    LockSystem: webdav.NewMemLS(),
//	}
func NewFS(fs afero.Fs) webdav.FileSystem {
	return &aferoFS{fs: fs}
}

// aferoFS adapts afero.Fs → webdav.FileSystem.
type aferoFS struct {
	fs afero.Fs
}

// Mkdir implements webdav.FileSystem.
func (a *aferoFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	return a.fs.Mkdir(cleanPath(name), perm)
}

// OpenFile implements webdav.FileSystem.
func (a *aferoFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	p := cleanPath(name)

	// Auto-create parent directories when creating a new file.
	if flag&os.O_CREATE != 0 {
		if dir := path.Dir(p); dir != "/" && dir != "." {
			if err := a.fs.MkdirAll(dir, 0755); err != nil {
				return nil, err
			}
		}
	}

	f, err := a.fs.OpenFile(p, flag, perm)
	if err != nil {
		return nil, err
	}
	return &aferoWebdavFile{File: f}, nil
}

// RemoveAll implements webdav.FileSystem.
func (a *aferoFS) RemoveAll(_ context.Context, name string) error {
	return a.fs.RemoveAll(cleanPath(name))
}

// Rename implements webdav.FileSystem.
func (a *aferoFS) Rename(_ context.Context, oldName, newName string) error {
	newP := cleanPath(newName)
	// Auto-create parent directories of the destination.
	if dir := path.Dir(newP); dir != "/" && dir != "." {
		if err := a.fs.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return a.fs.Rename(cleanPath(oldName), newP)
}

// Stat implements webdav.FileSystem.
func (a *aferoFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	return a.fs.Stat(cleanPath(name))
}

// ── aferoWebdavFile ──────────────────────────────────────────────────────────

// aferoWebdavFile wraps afero.File to satisfy webdav.File.
//
// webdav.File = http.File + io.Writer
// afero.File is a superset of both, so the wrapper is transparent.
type aferoWebdavFile struct {
	afero.File
}

// Readdir delegates to the underlying afero.File.
func (f *aferoWebdavFile) Readdir(count int) ([]os.FileInfo, error) {
	return f.File.Readdir(count)
}

// Stat delegates to the underlying afero.File.
func (f *aferoWebdavFile) Stat() (os.FileInfo, error) {
	return f.File.Stat()
}
