package aferodav_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lib-x/aferodav"
	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// memWebdavFS returns a webdav.FileSystem backed by webdav's own in-memory FS.
func memWebdavFS() webdav.FileSystem { return webdav.NewMemFS() }

// memAferoFS returns a fresh in-memory afero.Fs.
func memAferoFS() afero.Fs { return afero.NewMemMapFs() }

// ── webdav → afero (aferodav.New) ────────────────────────────────────────────

func TestNew_Mkdir(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	if err := afs.Mkdir("/dir", 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := afs.Stat("/dir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestNew_MkdirAll(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	if err := afs.MkdirAll("/a/b/c", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	info, err := afs.Stat("/a/b/c")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestNew_CreateAndRead(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, err := afs.Create("/hello.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.WriteString("hello webdav"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	f, err = afs.Open("/hello.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello webdav" {
		t.Errorf("got %q, want %q", got, "hello webdav")
	}
}

func TestNew_ReadAt(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/data.txt")
	f.WriteString("0123456789")
	f.Close()

	f, _ = afs.Open("/data.txt")
	defer f.Close()

	buf := make([]byte, 4)
	n, err := f.ReadAt(buf, 3)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf[:n]) != "3456" {
		t.Errorf("ReadAt got %q, want %q", buf[:n], "3456")
	}
}

func TestNew_WriteAt(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/patch.txt")
	f.WriteString("hello world")
	f.Close()

	f, _ = afs.OpenFile("/patch.txt", os.O_RDWR, 0644)
	defer f.Close()

	f.WriteAt([]byte("DAV  "), 6)

	f.Seek(0, io.SeekStart)
	got, _ := io.ReadAll(f)
	if string(got) != "hello DAV  " {
		t.Errorf("WriteAt got %q, want %q", got, "hello DAV  ")
	}
}

func TestNew_Readdirnames(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	afs.MkdirAll("/dir", 0755)
	for _, n := range []string{"a.txt", "b.txt"} {
		f, _ := afs.Create("/dir/" + n)
		f.Close()
	}

	dir, err := afs.Open("/dir")
	if err != nil {
		t.Fatalf("Open dir: %v", err)
	}
	defer dir.Close()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		t.Fatalf("Readdirnames: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("got %d names, want 2: %v", len(names), names)
	}
}

func TestNew_Truncate_Shrink(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/trunc.txt")
	f.WriteString("hello world")
	f.Close()

	f, _ = afs.OpenFile("/trunc.txt", os.O_RDWR, 0644)
	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	f.Close()

	f, _ = afs.Open("/trunc.txt")
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "hello" {
		t.Errorf("after truncate got %q, want %q", got, "hello")
	}
}

func TestNew_Truncate_Grow(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/grow.txt")
	f.WriteString("hi")
	f.Close()

	f, _ = afs.OpenFile("/grow.txt", os.O_RDWR, 0644)
	if err := f.Truncate(5); err != nil {
		t.Fatalf("Truncate grow: %v", err)
	}
	f.Close()

	info, _ := afs.Stat("/grow.txt")
	if info.Size() != 5 {
		t.Errorf("size = %d, want 5", info.Size())
	}
}

func TestNew_Remove(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/rm.txt")
	f.Close()

	if err := afs.Remove("/rm.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := afs.Stat("/rm.txt"); !os.IsNotExist(err) {
		t.Error("expected not-exist after Remove")
	}
}

func TestNew_RemoveDir_Fails(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	afs.Mkdir("/mydir", 0755)

	// Remove on a directory should fail (like os.Remove).
	if err := afs.Remove("/mydir"); err == nil {
		t.Error("expected error removing directory with Remove")
	}
}

func TestNew_RemoveAll(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	afs.MkdirAll("/tree/sub", 0755)
	f, _ := afs.Create("/tree/sub/file.txt")
	f.Close()

	if err := afs.RemoveAll("/tree"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := afs.Stat("/tree"); !os.IsNotExist(err) {
		t.Error("expected not-exist after RemoveAll")
	}
}

func TestNew_Rename(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())

	f, _ := afs.Create("/old.txt")
	f.WriteString("rename")
	f.Close()

	if err := afs.Rename("/old.txt", "/new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := afs.Stat("/old.txt"); !os.IsNotExist(err) {
		t.Error("old name should not exist")
	}
	if _, err := afs.Stat("/new.txt"); err != nil {
		t.Error("new name should exist")
	}
}

func TestNew_Sync(t *testing.T) {
	afs := aferodav.New(memWebdavFS(), context.Background())
	f, _ := afs.Create("/sync.txt")
	defer f.Close()
	// Sync is a no-op but must not error.
	if err := f.Sync(); err != nil {
		t.Errorf("Sync: %v", err)
	}
}

func TestNew_NilContext(t *testing.T) {
	// New should accept nil context and substitute Background().
	afs := aferodav.New(memWebdavFS(), nil)
	if err := afs.Mkdir("/nil-ctx", 0755); err != nil {
		t.Fatalf("Mkdir with nil ctx: %v", err)
	}
}

// ── afero → webdav (aferodav.NewFS) ─────────────────────────────────────────

func TestNewFS_Mkdir(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	if err := wfs.Mkdir(ctx, "/dir", 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	info, err := wfs.Stat(ctx, "/dir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestNewFS_OpenFileWriteRead(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	f, err := wfs.OpenFile(ctx, "/hello.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile write: %v", err)
	}
	f.Write([]byte("hello afero"))
	f.Close()

	f, err = wfs.OpenFile(ctx, "/hello.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile read: %v", err)
	}
	defer f.Close()

	got, _ := io.ReadAll(f)
	if string(got) != "hello afero" {
		t.Errorf("got %q, want %q", got, "hello afero")
	}
}

func TestNewFS_RemoveAll(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	wfs.Mkdir(ctx, "/d", 0755)
	wfs.Mkdir(ctx, "/d/sub", 0755)
	f, _ := wfs.OpenFile(ctx, "/d/sub/f.txt", os.O_CREATE|os.O_WRONLY, 0644)
	f.Close()

	if err := wfs.RemoveAll(ctx, "/d"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := wfs.Stat(ctx, "/d"); !os.IsNotExist(err) {
		t.Error("expected not-exist after RemoveAll")
	}
}

func TestNewFS_Rename(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	f, _ := wfs.OpenFile(ctx, "/a.txt", os.O_CREATE|os.O_WRONLY, 0644)
	f.Close()

	if err := wfs.Rename(ctx, "/a.txt", "/b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := wfs.Stat(ctx, "/a.txt"); !os.IsNotExist(err) {
		t.Error("old name should not exist")
	}
	if _, err := wfs.Stat(ctx, "/b.txt"); err != nil {
		t.Error("new name should exist")
	}
}

func TestNewFS_AutoMkdirOnCreate(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	f, err := wfs.OpenFile(ctx, "/deep/nested/file.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile nested: %v", err)
	}
	f.Close()

	if _, err := wfs.Stat(ctx, "/deep/nested/file.txt"); err != nil {
		t.Errorf("Stat nested: %v", err)
	}
}

func TestNewFS_Readdir(t *testing.T) {
	wfs := aferodav.NewFS(memAferoFS())
	ctx := context.Background()

	wfs.Mkdir(ctx, "/dir", 0755)
	for _, n := range []string{"x.txt", "y.txt", "z.txt"} {
		f, _ := wfs.OpenFile(ctx, "/dir/"+n, os.O_CREATE|os.O_WRONLY, 0644)
		f.Close()
	}

	dir, err := wfs.OpenFile(ctx, "/dir", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile dir: %v", err)
	}
	defer dir.Close()

	entries, err := dir.Readdir(-1)
	if err != nil {
		t.Fatalf("Readdir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("got %d entries, want 3", len(entries))
	}
}

// ── round-trip: afero → webdav → afero ──────────────────────────────────────

// TestRoundTrip verifies that data written via one adapter is readable via
// the other, using webdav.NewMemFS() as the shared backing store.
func TestRoundTrip(t *testing.T) {
	wdfs := webdav.NewMemFS()
	ctx := context.Background()

	// Write through the afero→webdav adapter.
	wfs := aferodav.NewFS(aferodav.New(wdfs, ctx))
	f, err := wfs.OpenFile(ctx, "/msg.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("write OpenFile: %v", err)
	}
	f.Write([]byte("round-trip"))
	f.Close()

	// Read back through the webdav→afero adapter.
	afs := aferodav.New(wdfs, ctx)
	rf, err := afs.Open("/msg.txt")
	if err != nil {
		t.Fatalf("read Open: %v", err)
	}
	defer rf.Close()

	got, _ := io.ReadAll(rf)
	if !strings.Contains(string(got), "round-trip") {
		t.Errorf("round-trip got %q", got)
	}
}
