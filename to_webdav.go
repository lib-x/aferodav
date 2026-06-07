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
// Without options, NewFS keeps webdav.FileSystem and os package semantics.
// Pass FSOption values to opt into convenience or object-store compatibility
// behavior such as parent directory creation, path validation, OpenFile flag
// rewriting, synthetic write Stat metadata, implicit directory Stat metadata,
// or recursive Mkdir.
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
		fs:                  fs,
		autoMkdirParents:    config.autoMkdirParents,
		recursiveMkdir:      config.recursiveMkdir,
		syntheticWriteStat:  config.syntheticWriteStat,
		pathCleaner:         config.pathCleaner,
		openFileFlagMappers: append([]OpenFileFlagMapper(nil), config.openFileFlagMappers...),
		statFallbacks:       append([]StatFallback(nil), config.statFallbacks...),
	}
}

// aferoFS adapts afero.Fs to webdav.FileSystem.
type aferoFS struct {
	fs                  afero.Fs
	autoMkdirParents    bool
	recursiveMkdir      bool
	syntheticWriteStat  bool
	pathCleaner         PathCleaner
	openFileFlagMappers []OpenFileFlagMapper
	statFallbacks       []StatFallback
}

// Mkdir implements webdav.FileSystem.
func (a *aferoFS) Mkdir(_ context.Context, name string, perm os.FileMode) error {
	p, err := a.cleanPath(PathOpMkdir, name)
	if err != nil {
		return err
	}
	if a.recursiveMkdir {
		return a.fs.MkdirAll(p, perm)
	}
	return a.fs.Mkdir(p, perm)
}

// OpenFile implements webdav.FileSystem.
func (a *aferoFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	p, err := a.cleanPath(PathOpOpenFile, name)
	if err != nil {
		return nil, err
	}
	flag, err = a.mapOpenFileFlag(p, flag)
	if err != nil {
		return nil, err
	}

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
	wf := &aferoWebdavFile{File: f}
	if a.syntheticWriteStat && isWritableFlag(flag) {
		return newSyntheticWriteStatFile(wf, p, flag, perm), nil
	}
	return wf, nil
}

// RemoveAll implements webdav.FileSystem.
func (a *aferoFS) RemoveAll(_ context.Context, name string) error {
	p, err := a.cleanPath(PathOpRemoveAll, name)
	if err != nil {
		return err
	}
	return a.fs.RemoveAll(p)
}

// Rename implements webdav.FileSystem.
func (a *aferoFS) Rename(_ context.Context, oldName, newName string) error {
	oldP, err := a.cleanPath(PathOpRenameSource, oldName)
	if err != nil {
		return err
	}
	newP, err := a.cleanPath(PathOpRenameDestination, newName)
	if err != nil {
		return err
	}
	if a.autoMkdirParents {
		if dir := path.Dir(newP); dir != "/" && dir != "." {
			if err := a.fs.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}
	}
	return a.fs.Rename(oldP, newP)
}

// Stat implements webdav.FileSystem.
func (a *aferoFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	p, err := a.cleanPath(PathOpStat, name)
	if err != nil {
		return nil, err
	}
	info, err := a.fs.Stat(p)
	if err == nil {
		return info, nil
	}
	for _, fallback := range a.statFallbacks {
		info, handled, fallbackErr := fallback(ctx, p, err)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		if handled {
			if info == nil {
				return nil, &os.PathError{Op: "stat", Path: p, Err: os.ErrInvalid}
			}
			return info, nil
		}
	}
	return nil, err
}

func (a *aferoFS) cleanPath(op, name string) (string, error) {
	return cleanPathWithCleaner(op, name, a.pathCleaner)
}

func (a *aferoFS) mapOpenFileFlag(name string, flag int) (int, error) {
	var err error
	for _, mapper := range a.openFileFlagMappers {
		flag, err = mapper(name, flag)
		if err != nil {
			return 0, err
		}
	}
	return flag, nil
}
