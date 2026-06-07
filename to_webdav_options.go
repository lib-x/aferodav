package aferodav

import (
	"context"
	"os"
)

// PathOp identifies which NewFS method is validating a path. The constants in
// this package cover every path-bearing webdav.FileSystem method.
type PathOp = string

const (
	// PathOpMkdir identifies a path passed to webdav.FileSystem.Mkdir.
	PathOpMkdir PathOp = "mkdir"
	// PathOpOpenFile identifies a path passed to webdav.FileSystem.OpenFile.
	PathOpOpenFile PathOp = "openfile"
	// PathOpRemoveAll identifies a path passed to webdav.FileSystem.RemoveAll.
	PathOpRemoveAll PathOp = "removeall"
	// PathOpRenameSource identifies the source path passed to webdav.FileSystem.Rename.
	PathOpRenameSource PathOp = "rename-source"
	// PathOpRenameDestination identifies the destination path passed to webdav.FileSystem.Rename.
	PathOpRenameDestination PathOp = "rename-destination"
	// PathOpStat identifies a path passed to webdav.FileSystem.Stat.
	PathOpStat PathOp = "stat"
)

// PathCleaner validates or rewrites a WebDAV path before aferodav applies its
// built-in path.Clean normalization. The name argument is the raw WebDAV path
// received by NewFS; return an error to reject it, or a replacement path to
// continue.
type PathCleaner func(op PathOp, name string) (string, error)

// OpenFileFlagMapper rewrites the flags passed to afero.Fs.OpenFile. The name
// argument is already normalized to absolute slash-separated form. Return an
// error to reject the open before it reaches the underlying filesystem.
type OpenFileFlagMapper func(name string, flag int) (int, error)

// StatFallback can synthesize a FileInfo when the underlying afero.Fs.Stat
// fails. The name argument is already normalized. Return handled=false to
// leave the original Stat error in place.
type StatFallback func(ctx context.Context, name string, err error) (info os.FileInfo, handled bool, fallbackErr error)

// ImplicitDirectoryStat can synthesize directory metadata for object stores
// that do not have real directory objects. The name argument is already
// normalized. Return handled=false when the path should still be treated as
// missing.
type ImplicitDirectoryStat func(name string) (info os.FileInfo, handled bool, err error)

// FSOption configures NewFS. Options are additive and opt-in; without options,
// NewFS preserves standard webdav.FileSystem and os package behavior.
type FSOption interface {
	apply(*fsOptions)
}

type fsOption func(*fsOptions)

func (f fsOption) apply(opts *fsOptions) {
	f(opts)
}

type fsOptions struct {
	autoMkdirParents    bool
	recursiveMkdir      bool
	syntheticWriteStat  bool
	pathCleaner         PathCleaner
	openFileFlagMappers []OpenFileFlagMapper
	statFallbacks       []StatFallback
}

// WithAutoMkdirParents changes create and rename behavior for NewFS.
//
// Default result: OpenFile with O_CREATE and Rename fail when the destination
// parent directory is missing.
//
// Enabled result: missing parent directories are created with MkdirAll before
// create or rename. This is convenient for clients that upload nested paths,
// but it is not enabled by default because webdav.FileSystem methods are
// expected to follow os package semantics.
func WithAutoMkdirParents() FSOption {
	return fsOption(func(opts *fsOptions) {
		opts.autoMkdirParents = true
	})
}

// WithPathCleaner changes path normalization behavior for NewFS.
//
// Default result: paths are normalized with path.Clean, so a path such as
// "/a/../b" becomes "/b".
//
// Enabled result: cleaner runs first and sees the raw path before path.Clean.
// It can reject or rewrite paths before normalization hides details such as
// "..", backslashes, Windows drive paths, or NUL bytes.
func WithPathCleaner(cleaner PathCleaner) FSOption {
	return fsOption(func(opts *fsOptions) {
		opts.pathCleaner = cleaner
	})
}

// WithOpenFileFlagMapper changes OpenFile flag handling for NewFS.
//
// Default result: OpenFile receives the exact flags supplied by x/net/webdav.
//
// Enabled result: mapper can rewrite flags or reject the open before NewFS
// calls afero.Fs.OpenFile. Multiple mappers run in option order.
func WithOpenFileFlagMapper(mapper OpenFileFlagMapper) FSOption {
	return fsOption(func(opts *fsOptions) {
		if mapper != nil {
			opts.openFileFlagMappers = append(opts.openFileFlagMappers, mapper)
		}
	})
}

// WithObjectStoreWriteMode changes OpenFile flags for object-store-like
// backends.
//
// Default result: PUT and COPY destinations from x/net/webdav are usually
// opened as O_RDWR|O_CREATE|O_TRUNC, and property updates may open existing
// resources as O_RDWR.
//
// Enabled result:
//   - O_RDWR opens with O_CREATE become O_WRONLY opens;
//   - non-create O_RDWR opens become read-only opens.
//
// This supports S3/R2/GCS-style backends that can stream writes or read
// objects, but cannot provide a read-write object handle. It is opt-in because
// normal filesystems support O_RDWR and should keep standard os.OpenFile
// semantics.
func WithObjectStoreWriteMode() FSOption {
	return WithOpenFileFlagMapper(func(_ string, flag int) (int, error) {
		return objectStoreWriteFlag(flag), nil
	})
}

// WithSyntheticWriteStat changes Stat behavior on writable files returned by
// NewFS.
//
// Default result: if a writable file's Stat fails during PUT, x/net/webdav
// fails the request before closing the file.
//
// Enabled result: writable handles track bytes written and return synthetic
// FileInfo when the underlying file's Stat fails. Successful underlying Stat
// results still take precedence. This helps object-store streaming writes where
// metadata may not be available until Close.
func WithSyntheticWriteStat() FSOption {
	return fsOption(func(opts *fsOptions) {
		opts.syntheticWriteStat = true
	})
}

// WithStatFallback changes NewFS.Stat behavior after the underlying
// filesystem returns an error.
//
// Default result: Stat returns the underlying afero.Fs.Stat error.
//
// Enabled result: fallback receives the normalized path and original error and
// may return replacement FileInfo. Return handled=false to keep the original
// error.
func WithStatFallback(fallback StatFallback) FSOption {
	return fsOption(func(opts *fsOptions) {
		if fallback != nil {
			opts.statFallbacks = append(opts.statFallbacks, fallback)
		}
	})
}

// WithImplicitDirectoryStat changes missing-path Stat behavior for NewFS.
//
// Default result: a missing path remains missing, even if an object-store
// backend has objects below that prefix.
//
// Enabled result: when the underlying Stat reports os.ErrNotExist, stat may
// synthesize directory FileInfo for the normalized path. Use this to represent
// object-store prefixes as WebDAV directories when listing shows child
// objects.
func WithImplicitDirectoryStat(stat ImplicitDirectoryStat) FSOption {
	return WithStatFallback(func(_ context.Context, name string, err error) (os.FileInfo, bool, error) {
		if stat == nil || !os.IsNotExist(err) {
			return nil, false, nil
		}
		return stat(name)
	})
}

// WithRecursiveMkdir changes NewFS.Mkdir behavior.
//
// Default result: Mkdir creates exactly one path segment and fails when parent
// directories are missing.
//
// Enabled result: Mkdir delegates to afero.Fs.MkdirAll, so WebDAV MKCOL can
// create missing parent prefixes. This is intended for backends where
// directories are implicit prefixes rather than real filesystem entries.
func WithRecursiveMkdir() FSOption {
	return fsOption(func(opts *fsOptions) {
		opts.recursiveMkdir = true
	})
}

func objectStoreWriteFlag(flag int) int {
	if flag&accessModeMask != os.O_RDWR {
		return flag
	}

	if flag&os.O_CREATE != 0 {
		return (flag &^ accessModeMask) | os.O_WRONLY
	}

	const writeOnlyFlags = os.O_APPEND | os.O_EXCL | os.O_TRUNC
	return flag &^ (accessModeMask | writeOnlyFlags)
}
