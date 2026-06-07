package aferodav

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

const zeroChunkSize = 32 * 1024

// davFile wraps a webdav.File to satisfy the fuller afero.File interface.
//
// webdav.File = http.File (Close/Read/Seek/Readdir/Stat) + io.Writer.
//
// ReadAt and WriteAt are emulated via Seek+Read/Write and protected by mu so
// they satisfy io.ReaderAt/io.WriterAt offset semantics. Truncate is emulated by
// rewriting the file because WebDAV has no truncate primitive.
type davFile struct {
	mu     sync.Mutex
	wf     webdav.File
	name   string
	fs     webdav.FileSystem
	ctx    context.Context
	flag   int
	closed bool
}

const accessModeMask = os.O_WRONLY | os.O_RDWR

func (f *davFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return afero.ErrFileClosed
	}
	err := f.wf.Close()
	f.closed = true
	return err
}

func (f *davFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireReadable("read"); err != nil {
		return 0, err
	}
	return f.wf.Read(p)
}

func (f *davFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireOpen(); err != nil {
		return 0, err
	}
	return f.wf.Seek(offset, whence)
}

func (f *davFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireWritable("write"); err != nil {
		return 0, err
	}
	n, err := f.wf.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (f *davFile) Readdir(count int) ([]os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireReadable("readdir"); err != nil {
		return nil, err
	}
	return f.wf.Readdir(count)
}

func (f *davFile) Stat() (os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireOpen(); err != nil {
		return nil, err
	}
	return f.wf.Stat()
}

func (f *davFile) Name() string { return f.name }

// ReadAt implements io.ReaderAt by seeking to offset, reading, then restoring
// the original position.
func (f *davFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireReadable("read"); err != nil {
		return 0, err
	}
	cur, err := f.wf.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if _, err := f.wf.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, readErr := io.ReadFull(f.wf, p)
	if _, seekErr := f.wf.Seek(cur, io.SeekStart); seekErr != nil && readErr == nil {
		return n, seekErr
	}
	if readErr == io.ErrUnexpectedEOF {
		readErr = io.EOF
	}
	return n, readErr
}

// WriteAt implements io.WriterAt by seeking to offset, writing, then restoring
// the original position.
func (f *davFile) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireWritable("write"); err != nil {
		return 0, err
	}
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
	if writeErr == nil && n < len(p) {
		writeErr = io.ErrShortWrite
	}
	return n, writeErr
}

// Readdirnames implements afero.File by delegating to Readdir.
func (f *davFile) Readdirnames(n int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireReadable("readdirnames"); err != nil {
		return nil, err
	}
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
func (f *davFile) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.requireWritable("truncate"); err != nil {
		return err
	}
	if size < 0 {
		return &os.PathError{Op: "truncate", Path: f.name, Err: os.ErrInvalid}
	}

	offset, err := f.wf.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}

	info, err := f.wf.Stat()
	if err != nil {
		return err
	}
	currentSize := info.Size()
	if size == currentSize {
		return nil
	}

	if size > currentSize {
		if _, err := f.wf.Seek(0, io.SeekEnd); err != nil {
			return err
		}
		if err := writeZeros(f.wf, size-currentSize); err != nil {
			return err
		}
		_, err := f.wf.Seek(offset, io.SeekStart)
		return err
	}

	if _, err := f.wf.Seek(0, io.SeekStart); err != nil {
		return err
	}
	buf, err := io.ReadAll(io.LimitReader(f.wf, size))
	if err != nil {
		return err
	}
	if int64(len(buf)) != size {
		return io.ErrUnexpectedEOF
	}
	if err := f.rewrite(buf); err != nil {
		return err
	}
	_, err = f.wf.Seek(offset, io.SeekStart)
	return err
}

func (f *davFile) rewrite(data []byte) error {
	newFile, err := f.fs.OpenFile(f.ctx, f.name, os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}

	n, writeErr := newFile.Write(data)
	if writeErr == nil && n < len(data) {
		writeErr = io.ErrShortWrite
	}
	closeErr := newFile.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	reopened, err := f.fs.OpenFile(f.ctx, f.name, f.reopenFlag(), 0666)
	if err != nil {
		return err
	}
	_ = f.wf.Close()
	f.wf = reopened
	return nil
}

func writeZeros(w io.Writer, n int64) error {
	var zeros [zeroChunkSize]byte
	for n > 0 {
		chunk := len(zeros)
		if n < int64(chunk) {
			chunk = int(n)
		}
		written, err := w.Write(zeros[:chunk])
		if err != nil {
			return err
		}
		if written != chunk {
			return io.ErrShortWrite
		}
		n -= int64(written)
	}
	return nil
}

func (f *davFile) requireOpen() error {
	if f.closed {
		return afero.ErrFileClosed
	}
	return nil
}

func (f *davFile) requireReadable(op string) error {
	if err := f.requireOpen(); err != nil {
		return err
	}
	if f.flag&accessModeMask == os.O_WRONLY {
		return &os.PathError{Op: op, Path: f.name, Err: os.ErrPermission}
	}
	return nil
}

func (f *davFile) requireWritable(op string) error {
	if err := f.requireOpen(); err != nil {
		return err
	}
	if f.flag&accessModeMask == os.O_RDONLY {
		return &os.PathError{Op: op, Path: f.name, Err: os.ErrPermission}
	}
	return nil
}

func (f *davFile) reopenFlag() int {
	return f.flag & (accessModeMask | os.O_APPEND)
}

// Sync is a no-op: WebDAV has no fsync equivalent.
func (f *davFile) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.requireOpen()
}

// WriteString implements afero.File.
func (f *davFile) WriteString(s string) (int, error) {
	return f.Write([]byte(s))
}
