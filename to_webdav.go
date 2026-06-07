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
func NewFS(fs afero.Fs, opts ...FSOption) webdav.FileSystem {
	config := fsOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt.apply(&config)
		}
	}
	return &aferoFS{
		fs:               fs,
		autoMkdirParents: config.autoMkdirParents,
	}
}

// FSOption configures the afero-to-WebDAV adapter.
type FSOption interface {
	apply(*fsOptions)
}

type fsOption func(*fsOptions)

func (f fsOption) apply(opts *fsOptions) {
	f(opts)
}

type fsOptions struct {
	autoMkdirParents bool
}

// WithAutoMkdirParents creates missing parent directories on OpenFile(O_CREATE)
// and Rename. NewFS does not enable this by default because webdav.FileSystem
// methods are expected to follow os package semantics.
func WithAutoMkdirParents() FSOption {
	return fsOption(func(opts *fsOptions) {
		opts.autoMkdirParents = true
	})
}

// aferoFS adapts afero.Fs to webdav.FileSystem.
type aferoFS struct {
	fs               afero.Fs
	autoMkdirParents bool
}

// Mkdir implements webdav.FileSystem.
func (a *aferoFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	return a.fs.Mkdir(cleanPath(name), perm)
}

// OpenFile implements webdav.FileSystem.
func (a *aferoFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	p := cleanPath(name)

	if a.autoMkdirParents && flag&os.O_CREATE != 0 {
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
	if a.autoMkdirParents {
		if dir := path.Dir(newP); dir != "/" && dir != "." {
			if err := a.fs.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}
	}
	return a.fs.Rename(cleanPath(oldName), newP)
}

// Stat implements webdav.FileSystem.
func (a *aferoFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	return a.fs.Stat(cleanPath(name))
}

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
