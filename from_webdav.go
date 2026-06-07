// Package aferodav provides bidirectional adapters between
// golang.org/x/net/webdav.FileSystem and github.com/spf13/afero.Fs.
//
// Usage:
//
//	var wdfs webdav.FileSystem = myWebDAVBackend()
//	var afs  afero.Fs         = aferodav.New(wdfs, context.Background())
//
//	// now use afs with any afero-aware library
//	data, _ := afero.ReadFile(afs, "/notes/hello.txt")
package aferodav

import (
	"context"
	"errors"
	"os"
	"path"
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
	return d.fs.Mkdir(d.ctx, cleanPath(name), perm)
}

// MkdirAll implements afero.Fs by walking the path and creating each missing
// segment. webdav.FileSystem has no MkdirAll, so we emulate it.
func (d *davFs) MkdirAll(p string, perm os.FileMode) error {
	p = cleanPath(p)
	if p == "/" {
		return nil
	}

	// Collect segments to create from deepest to root, stop when one exists.
	missing := []string{}
	cur := p
	for cur != "/" {
		info, err := d.fs.Stat(d.ctx, cur)
		if err == nil {
			if !info.IsDir() {
				return &os.PathError{Op: "mkdir", Path: cur, Err: os.ErrExist}
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, cur)
		cur = path.Dir(cur)
	}

	// Create from shallowest to deepest.
	for i := len(missing) - 1; i >= 0; i-- {
		if err := d.fs.Mkdir(d.ctx, missing[i], perm); err != nil {
			if errors.Is(err, os.ErrExist) {
				info, statErr := d.fs.Stat(d.ctx, missing[i])
				if statErr == nil && info.IsDir() {
					continue
				}
			}
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
	p := cleanPath(name)
	wf, err := d.fs.OpenFile(d.ctx, p, flag, perm)
	if err != nil {
		return nil, err
	}
	return &davFile{wf: wf, name: p, fs: d.fs, ctx: d.ctx}, nil
}

// Remove implements afero.Fs. WebDAV RemoveAll on a file behaves like Remove.
func (d *davFs) Remove(name string) error {
	p := cleanPath(name)
	// Guard: do not remove a directory with Remove (mimic os.Remove behaviour).
	info, err := d.fs.Stat(d.ctx, p)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &os.PathError{Op: "remove", Path: p, Err: os.ErrInvalid}
	}
	return d.fs.RemoveAll(d.ctx, p)
}

// RemoveAll implements afero.Fs.
func (d *davFs) RemoveAll(p string) error {
	err := d.fs.RemoveAll(d.ctx, cleanPath(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Rename implements afero.Fs.
func (d *davFs) Rename(oldname, newname string) error {
	return d.fs.Rename(d.ctx, cleanPath(oldname), cleanPath(newname))
}

// Stat implements afero.Fs.
func (d *davFs) Stat(name string) (os.FileInfo, error) {
	return d.fs.Stat(d.ctx, cleanPath(name))
}

// Chmod is a no-op: WebDAV does not expose chmod semantics.
func (d *davFs) Chmod(name string, mode os.FileMode) error { return nil }

// Chown is a no-op: WebDAV does not expose chown semantics.
func (d *davFs) Chown(name string, uid, gid int) error { return nil }

// Chtimes is a no-op for the same reason (WebDAV PROPPATCH could implement
// this but most backends don't support it reliably via the FileSystem API).
func (d *davFs) Chtimes(name string, atime time.Time, mtime time.Time) error { return nil }
