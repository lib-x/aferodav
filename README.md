# aferodav

`github.com/lib-x/aferodav` — bidirectional adapters between
[`golang.org/x/net/webdav`](https://pkg.go.dev/golang.org/x/net/webdav) and
[`github.com/spf13/afero`](https://github.com/spf13/afero).

## Two adapters

### `New` — webdav.FileSystem → afero.Fs

Wrap any `webdav.FileSystem` so it can be used wherever `afero.Fs` is expected.

```go
var wdfs webdav.FileSystem = myWebDAVBackend()
afs := aferodav.New(wdfs, context.Background())

// use with any afero-aware library
data, _ := afero.ReadFile(afs, "/notes/hello.txt")
```

### `NewFS` — afero.Fs → webdav.FileSystem

Wrap any `afero.Fs` to serve it over WebDAV.

```go
afs := afero.NewMemMapFs() // or OsFs, BasePathFs, your R2Fs, …
handler := &webdav.Handler{
    FileSystem: aferodav.NewFS(afs),
    LockSystem: webdav.NewMemLS(),
}
http.ListenAndServe(":8080", handler)
```

## Installation

```bash
go get github.com/lib-x/aferodav
```

## Supported afero backends

Any `afero.Fs` works with `NewFS`:

| Backend | Notes |
|---------|-------|
| `afero.NewMemMapFs()` | In-memory, great for tests |
| `afero.NewOsFs()` | Real OS filesystem |
| `afero.NewBasePathFs(base, dir)` | Chroot-like sandbox |
| `afero.NewReadOnlyFs(base)` | Read-only view |
| `afero.NewCopyOnWriteFs(base, overlay)` | Overlay writes on a read-only base |
| Custom (S3, GCS, R2, SFTP …) | Anything implementing `afero.Fs` |

## Behaviour notes

| Feature | New (webdav→afero) | NewFS (afero→webdav) |
|---|---|---|
| `MkdirAll` | Emulated via repeated `Mkdir` | Delegates to `afero.Fs.MkdirAll` |
| Auto-create parent dirs | — | Disabled by default; enable with `WithAutoMkdirParents()` |
| `ReadAt` / `WriteAt` | Emulated via locked `Seek` + `Read`/`Write` | Delegates to afero |
| `Truncate` | Emulated (shrink: copy+rewrite) | Delegates to afero |
| `Sync` | No-op | Delegates to afero |
| `Chmod` / `Chown` / `Chtimes` | Unsupported (not in WebDAV) | — |

```go
// Optional convenience mode: create missing parents on WebDAV create/rename.
handler := &webdav.Handler{
    FileSystem: aferodav.NewFS(afs, aferodav.WithAutoMkdirParents()),
    LockSystem: webdav.NewMemLS(),
}
```

## Running the example

```bash
# Serve an in-memory afero.Fs over WebDAV
go run ./example -mode afero-to-webdav -addr :8080

# Use a webdav.FileSystem as an afero.Fs (writes a file, then serves via HTTP)
go run ./example -mode webdav-to-afero -addr :8080
```

## Testing

```bash
go test ./...
```

## License

MIT
