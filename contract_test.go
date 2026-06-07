package aferodav_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/lib-x/aferodav"
	"golang.org/x/net/webdav"
)

func TestNew_RelativePathIsNormalized(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("relative.txt")
	if err != nil {
		t.Fatalf("Create relative path: %v", err)
	}
	if _, err := f.WriteString("relative"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err = afs.Open("/relative.txt")
	if err != nil {
		t.Fatalf("Open absolute path: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "relative" {
		t.Fatalf("content = %q, want %q", got, "relative")
	}
}

func TestNew_MkdirAllExistingFileFails(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("/file")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := afs.MkdirAll("/file", 0755); err == nil {
		t.Fatal("MkdirAll existing file = nil, want error")
	}
}

func TestNew_RemoveAllMissingPathSucceeds(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	if err := afs.RemoveAll("/missing/child"); err != nil {
		t.Fatalf("RemoveAll missing nested path: %v", err)
	}
}

func TestNew_TruncateNegativeFails(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("/file")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()

	if err := f.Truncate(-1); err == nil {
		t.Fatal("Truncate(-1) = nil, want error")
	} else if !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("Truncate(-1) = %v, want invalid error", err)
	}
}

func TestNew_TruncateGrowKeepsOffsetAndWritesZeroes(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("/grow.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.WriteString("hi"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if _, err := f.Seek(1, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate grow: %v", err)
	}
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek current: %v", err)
	}
	if offset != 1 {
		t.Fatalf("offset = %d, want 1", offset)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err = afs.Open("/grow.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{'h', 'i', 0, 0, 0}
	if string(got) != string(want) {
		t.Fatalf("content = %v, want %v", got, want)
	}
}

func TestNew_TruncateShrinkKeepsOffset(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("/shrink.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.WriteString("hello world"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if _, err := f.Seek(3, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate shrink: %v", err)
	}
	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek current: %v", err)
	}
	if offset != 3 {
		t.Fatalf("offset = %d, want 3", offset)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err = afs.Open("/shrink.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
}
