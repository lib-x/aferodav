package aferodav_test

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib-x/aferodav"
	"github.com/spf13/afero"
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

func TestNew_OpenReturnsReadOnlyFile(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	if err := afero.WriteFile(afs, "/readonly.txt", []byte("content"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f, err := afs.Open("/readonly.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte("x")); !os.IsPermission(err) {
		t.Fatalf("Write on read-only file = %v, want permission error", err)
	}
	if _, err := f.WriteAt([]byte("x"), 0); !os.IsPermission(err) {
		t.Fatalf("WriteAt on read-only file = %v, want permission error", err)
	}
	if err := f.Truncate(0); !os.IsPermission(err) {
		t.Fatalf("Truncate on read-only file = %v, want permission error", err)
	}
}

func TestNew_FileClosedFails(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	f, err := afs.Create("/closed.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := f.Read(make([]byte, 1)); !errors.Is(err, afero.ErrFileClosed) {
		t.Fatalf("Read after Close = %v, want ErrFileClosed", err)
	}
	if _, err := f.Write([]byte("x")); !errors.Is(err, afero.ErrFileClosed) {
		t.Fatalf("Write after Close = %v, want ErrFileClosed", err)
	}
	if _, err := f.Stat(); !errors.Is(err, afero.ErrFileClosed) {
		t.Fatalf("Stat after Close = %v, want ErrFileClosed", err)
	}
}

func TestNew_ConcurrentReadAtIsStable(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())
	content := []byte("0123456789abcdefghijklmnopqrstuvwxyz")

	if err := afero.WriteFile(afs, "/data.txt", content, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	f, err := afs.Open("/data.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	if _, err := f.Seek(7, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			off := int64(i % (len(content) - 4))
			want := content[off : off+4]
			for range 100 {
				buf := make([]byte, 4)
				if _, err := f.ReadAt(buf, off); err != nil {
					errs <- err
					return
				}
				if string(buf) != string(want) {
					errs <- errors.New("ReadAt returned unstable data")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	offset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek current: %v", err)
	}
	if offset != 7 {
		t.Fatalf("offset = %d, want 7", offset)
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

func TestNew_MetadataMutationsAreUnsupported(t *testing.T) {
	afs := aferodav.New(webdav.NewMemFS(), context.Background())

	if err := afs.Chmod("/missing", 0644); !os.IsPermission(err) {
		t.Fatalf("Chmod = %v, want permission error", err)
	}
	if err := afs.Chown("/missing", 1, 1); !os.IsPermission(err) {
		t.Fatalf("Chown = %v, want permission error", err)
	}
	now := time.Now()
	if err := afs.Chtimes("/missing", now, now); !os.IsPermission(err) {
		t.Fatalf("Chtimes = %v, want permission error", err)
	}
}

func TestNewFS_DoesNotAutoMkdirParentsByDefault(t *testing.T) {
	wfs := aferodav.NewFS(osBackedAferoFS(t))
	ctx := context.Background()

	if _, err := wfs.OpenFile(ctx, "/deep/nested/file.txt", os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		t.Fatal("OpenFile nested create = nil, want missing parent error")
	}

	f, err := wfs.OpenFile(ctx, "/file.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile source: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close source: %v", err)
	}

	if err := wfs.Rename(ctx, "/file.txt", "/missing/file.txt"); err == nil {
		t.Fatal("Rename to missing parent = nil, want error")
	}
}

func TestNewFS_WithAutoMkdirParents(t *testing.T) {
	wfs := aferodav.NewFS(osBackedAferoFS(t), aferodav.WithAutoMkdirParents())
	ctx := context.Background()

	f, err := wfs.OpenFile(ctx, "/deep/nested/file.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile nested create: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close nested: %v", err)
	}

	f, err = wfs.OpenFile(ctx, "/source.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile source: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close source: %v", err)
	}
	if err := wfs.Rename(ctx, "/source.txt", "/renamed/path/file.txt"); err != nil {
		t.Fatalf("Rename with auto mkdir: %v", err)
	}
	if _, err := wfs.Stat(ctx, "/renamed/path/file.txt"); err != nil {
		t.Fatalf("Stat renamed file: %v", err)
	}
}

func osBackedAferoFS(t *testing.T) afero.Fs {
	t.Helper()
	return afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
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
