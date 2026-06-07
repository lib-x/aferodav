package aferodav_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib-x/aferodav"
	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

func TestNewFS_WithPathCleanerSeesRawPathBeforeClean(t *testing.T) {
	ctx := context.Background()
	var seenOp, seenName string

	wfs := aferodav.NewFS(memAferoFS(), aferodav.WithPathCleaner(func(op, name string) (string, error) {
		seenOp = op
		seenName = name
		if name == "/safe/../secret.txt" {
			return "", &os.PathError{Op: op, Path: name, Err: os.ErrPermission}
		}
		return name, nil
	}))

	if _, err := wfs.OpenFile(ctx, "/safe/../secret.txt", os.O_CREATE|os.O_WRONLY, 0644); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("OpenFile rejected path = %v, want permission error", err)
	}
	if seenOp != aferodav.PathOpOpenFile {
		t.Fatalf("path cleaner op = %q, want %q", seenOp, aferodav.PathOpOpenFile)
	}
	if seenName != "/safe/../secret.txt" {
		t.Fatalf("path cleaner name = %q, want raw path", seenName)
	}
}

func TestNewFS_WithOpenFileFlagMapperError(t *testing.T) {
	ctx := context.Background()
	errMapper := errors.New("mapper rejected flags")
	wfs := aferodav.NewFS(memAferoFS(), aferodav.WithOpenFileFlagMapper(func(string, int) (int, error) {
		return 0, errMapper
	}))

	if _, err := wfs.OpenFile(ctx, "/file.txt", os.O_RDONLY, 0); !errors.Is(err, errMapper) {
		t.Fatalf("OpenFile mapper error = %v, want %v", err, errMapper)
	}
}

func TestNewFS_WithObjectStoreWriteModeMapsCreateToWriteOnly(t *testing.T) {
	ctx := context.Background()
	rfs := &recordingOpenFileFs{Fs: memAferoFS()}
	wfs := aferodav.NewFS(rfs, aferodav.WithObjectStoreWriteMode())

	f, err := wfs.OpenFile(ctx, "/put.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile create: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if got := rfs.flags[0]; got != want {
		t.Fatalf("OpenFile flag = %#x, want %#x", got, want)
	}
}

func TestNewFS_WithObjectStoreWriteModeMapsNonCreateReadWriteToReadOnly(t *testing.T) {
	ctx := context.Background()
	rfs := &recordingOpenFileFs{Fs: memAferoFS()}
	if err := afero.WriteFile(rfs.Fs, "/existing.txt", []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	wfs := aferodav.NewFS(rfs, aferodav.WithObjectStoreWriteMode())

	f, err := wfs.OpenFile(ctx, "/existing.txt", os.O_RDWR|os.O_APPEND|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("OpenFile existing: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := rfs.flags[0]; got != os.O_RDONLY {
		t.Fatalf("OpenFile flag = %#x, want O_RDONLY", got)
	}
}

func TestNewFS_WithSyntheticWriteStat(t *testing.T) {
	ctx := context.Background()
	wfs := aferodav.NewFS(statFailingOpenFileFs{Fs: memAferoFS()}, aferodav.WithSyntheticWriteStat())

	f, err := wfs.OpenFile(ctx, "/file.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer f.Close()

	if n, err := f.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write = %d, %v; want 6, nil", n, err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("synthetic Stat: %v", err)
	}
	if info.Name() != "file.txt" {
		t.Fatalf("Name = %q, want file.txt", info.Name())
	}
	if info.Size() != 6 {
		t.Fatalf("Size = %d, want 6", info.Size())
	}
	if info.Mode()&os.ModePerm != 0644 {
		t.Fatalf("Mode = %v, want 0644 permissions", info.Mode())
	}
	if info.ModTime().IsZero() {
		t.Fatal("ModTime is zero")
	}
}

func TestNewFS_WithSyntheticWriteStatAllowsWebDAVPutBeforeClose(t *testing.T) {
	backend := statFailingOpenFileFs{Fs: memAferoFS()}
	withoutSynthetic := &webdav.Handler{
		FileSystem: aferodav.NewFS(backend),
		LockSystem: webdav.NewMemLS(),
	}

	rec := httptest.NewRecorder()
	withoutSynthetic.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/without.txt", strings.NewReader("content")))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT without synthetic Stat status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	withSynthetic := &webdav.Handler{
		FileSystem: aferodav.NewFS(backend, aferodav.WithSyntheticWriteStat()),
		LockSystem: webdav.NewMemLS(),
	}
	rec = httptest.NewRecorder()
	withSynthetic.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/with.txt", strings.NewReader("content")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("PUT with synthetic Stat status = %d, want %d; body=%q", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if got, err := afero.ReadFile(backend.Fs, "/with.txt"); err != nil || string(got) != "content" {
		t.Fatalf("stored PUT content = %q, %v; want content, nil", got, err)
	}
}

func TestNewFS_WithImplicitDirectoryStat(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	wfs := aferodav.NewFS(memAferoFS(), aferodav.WithImplicitDirectoryStat(func(name string) (os.FileInfo, bool, error) {
		if name != "/photos" {
			return nil, false, nil
		}
		return testFileInfo{name: "photos", mode: os.ModeDir | 0755, modTime: now}, true, nil
	}))

	info, err := wfs.Stat(ctx, "/photos")
	if err != nil {
		t.Fatalf("Stat implicit directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("implicit FileInfo is not a directory")
	}
	if info.Name() != "photos" {
		t.Fatalf("Name = %q, want photos", info.Name())
	}

	if _, err := wfs.Stat(ctx, "/missing"); !os.IsNotExist(err) {
		t.Fatalf("Stat missing = %v, want not-exist", err)
	}
}

func TestNewFS_WithRecursiveMkdir(t *testing.T) {
	ctx := context.Background()
	wfs := aferodav.NewFS(osBackedAferoFS(t), aferodav.WithRecursiveMkdir())

	if err := wfs.Mkdir(ctx, "/deep/nested/dir", 0755); err != nil {
		t.Fatalf("Mkdir recursive: %v", err)
	}
	info, err := wfs.Stat(ctx, "/deep/nested/dir")
	if err != nil {
		t.Fatalf("Stat recursive dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("recursive Mkdir target is not a directory")
	}
}

type recordingOpenFileFs struct {
	afero.Fs
	flags []int
}

func (fs *recordingOpenFileFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	fs.flags = append(fs.flags, flag)
	return fs.Fs.OpenFile(name, flag, perm)
}

type statFailingOpenFileFs struct {
	afero.Fs
}

func (fs statFailingOpenFileFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return statFailingFile{File: f, name: name}, nil
}

type statFailingFile struct {
	afero.File
	name string
}

func (f statFailingFile) Stat() (os.FileInfo, error) {
	return nil, &os.PathError{Op: "stat", Path: f.name, Err: os.ErrNotExist}
}

type testFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (f testFileInfo) Name() string       { return f.name }
func (f testFileInfo) Size() int64        { return f.size }
func (f testFileInfo) Mode() os.FileMode  { return f.mode }
func (f testFileInfo) ModTime() time.Time { return f.modTime }
func (f testFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f testFileInfo) Sys() any           { return nil }
