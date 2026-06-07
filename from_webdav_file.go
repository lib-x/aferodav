package aferodav

import (
	"context"
	"io"
	"os"

	"golang.org/x/net/webdav"
)

const zeroChunkSize = 32 * 1024

// davFile wraps a webdav.File to satisfy the fuller afero.File interface.
//
// webdav.File = http.File (Close/Read/Seek/Readdir/Stat) + io.Writer.
//
// ReadAt and WriteAt are emulated via Seek+Read/Write. They preserve the
// original offset, but callers should not use them concurrently on one handle.
// Truncate is emulated by rewriting the file because WebDAV has no truncate
// primitive.
type davFile struct {
	wf   webdav.File
	name string
	fs   webdav.FileSystem
	ctx  context.Context
}

func (f *davFile) Close() error                                 { return f.wf.Close() }
func (f *davFile) Read(p []byte) (int, error)                   { return f.wf.Read(p) }
func (f *davFile) Seek(offset int64, whence int) (int64, error) { return f.wf.Seek(offset, whence) }
func (f *davFile) Write(p []byte) (int, error)                  { return f.wf.Write(p) }
func (f *davFile) Readdir(count int) ([]os.FileInfo, error)     { return f.wf.Readdir(count) }
func (f *davFile) Stat() (os.FileInfo, error)                   { return f.wf.Stat() }
func (f *davFile) Name() string                                 { return f.name }

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

	reopened, err := f.fs.OpenFile(f.ctx, f.name, os.O_RDWR, 0666)
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

// Sync is a no-op: WebDAV has no fsync equivalent.
func (f *davFile) Sync() error { return nil }

// WriteString implements afero.File.
func (f *davFile) WriteString(s string) (int, error) {
	return f.wf.Write([]byte(s))
}
