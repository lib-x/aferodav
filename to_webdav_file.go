package aferodav

import (
	"os"
	"sync"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

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

type syntheticWriteStatFile struct {
	webdav.File

	mu          sync.Mutex
	name        string
	mode        os.FileMode
	appendMode  bool
	size        int64
	sizeKnown   bool
	offset      int64
	offsetKnown bool
	modTime     time.Time
}

func newSyntheticWriteStatFile(f webdav.File, name string, flag int, perm os.FileMode) webdav.File {
	sf := &syntheticWriteStatFile{
		File:        f,
		name:        name,
		mode:        regularFileMode(perm),
		appendMode:  flag&os.O_APPEND != 0,
		offsetKnown: true,
		modTime:     time.Now(),
	}

	if flag&os.O_TRUNC != 0 {
		sf.sizeKnown = true
	}
	if info, err := f.Stat(); err == nil {
		sf.size = info.Size()
		sf.sizeKnown = true
		sf.modTime = info.ModTime()
		sf.mode = info.Mode()
	}
	if sf.appendMode {
		if sf.sizeKnown {
			sf.offset = sf.size
		} else {
			sf.offsetKnown = false
		}
	}

	return sf
}

func (f *syntheticWriteStatFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, err := f.File.Write(p)
	if n > 0 {
		f.modTime = time.Now()
		if f.offsetKnown {
			if f.appendMode && f.sizeKnown {
				f.offset = f.size
			}
			end := f.offset + int64(n)
			f.offset = end
			if !f.sizeKnown || end > f.size {
				f.size = end
				f.sizeKnown = true
			}
		}
	}
	return n, err
}

func (f *syntheticWriteStatFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	newOffset, err := f.File.Seek(offset, whence)
	if err != nil {
		f.offsetKnown = false
		return newOffset, err
	}
	f.offset = newOffset
	f.offsetKnown = true
	return newOffset, nil
}

func (f *syntheticWriteStatFile) Stat() (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	info, err := f.File.Stat()
	if err == nil {
		return info, nil
	}

	if !f.sizeKnown {
		return nil, err
	}
	return syntheticFileInfo{
		name:    fileInfoName(f.name),
		size:    f.size,
		mode:    f.mode,
		modTime: f.modTime,
	}, nil
}

type syntheticFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (f syntheticFileInfo) Name() string       { return f.name }
func (f syntheticFileInfo) Size() int64        { return f.size }
func (f syntheticFileInfo) Mode() os.FileMode  { return f.mode }
func (f syntheticFileInfo) ModTime() time.Time { return f.modTime }
func (f syntheticFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f syntheticFileInfo) Sys() any           { return nil }

func isWritableFlag(flag int) bool {
	return flag&accessModeMask != os.O_RDONLY
}

func regularFileMode(perm os.FileMode) os.FileMode {
	mode := perm & os.ModePerm
	if mode == 0 {
		mode = 0666
	}
	return mode
}
