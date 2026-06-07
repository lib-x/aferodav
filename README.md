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
| Recursive WebDAV `MKCOL` | — | Disabled by default; enable with `WithRecursiveMkdir()` |
| Path validation before `path.Clean` | Uses built-in normalization | Disabled by default; enable with `WithPathCleaner(...)` |
| OpenFile flag rewriting | — | Disabled by default; enable with `WithOpenFileFlagMapper(...)` or `WithObjectStoreWriteMode()` |
| Synthetic Stat for streaming writes | — | Disabled by default; enable with `WithSyntheticWriteStat()` |
| Stat fallback / implicit directories | — | Disabled by default; enable with `WithStatFallback(...)` or `WithImplicitDirectoryStat(...)` |
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

## `NewFS` options

`NewFS` keeps normal `webdav.FileSystem` / `os` semantics by default. The
options below are opt-in because object stores and regular filesystems need
different behavior.

| Option | Default result | Enabled result |
|---|---|---|
| `WithAutoMkdirParents()` | `OpenFile(O_CREATE)` and `Rename` fail when the destination parent is missing. | Missing parent directories are created before create or rename. |
| `WithPathCleaner(cleaner)` | Paths are normalized with `path.Clean`; for example `/a/../b` becomes `/b`. | `cleaner(op, rawName)` runs before normalization and can reject or rewrite the raw path. Use this to preserve stricter path safety rules such as rejecting `..`, `\`, Windows drive paths, or NUL bytes. |
| `WithOpenFileFlagMapper(mapper)` | `OpenFile` receives the exact flags supplied by `x/net/webdav`. | `mapper(normalizedName, flag)` can rewrite flags or return an error before the underlying `afero.Fs.OpenFile` call. Multiple mappers run in option order. |
| `WithObjectStoreWriteMode()` | PUT/COPY destinations are opened as `O_RDWR\|O_CREATE\|O_TRUNC`; non-create property writes may open as `O_RDWR`. | Create/write opens using `O_RDWR` are rewritten to `O_WRONLY`; non-create `O_RDWR` opens are downgraded to `O_RDONLY`. This helps S3/R2-like backends that do not support read-write object handles. |
| `WithSyntheticWriteStat()` | If a writable file's `Stat()` fails during PUT, the WebDAV PUT fails before `Close()`. | Writable handles track bytes written and return synthetic `FileInfo` when the underlying `Stat()` fails. Existing successful `Stat()` results still win. |
| `WithStatFallback(fallback)` | `Stat` returns the underlying filesystem error. | `fallback(ctx, normalizedName, originalErr)` may return replacement `FileInfo`, or decline and keep the original error. |
| `WithImplicitDirectoryStat(stat)` | A missing path is missing, even if object keys exist under that prefix. | When the underlying `Stat` reports not-exist, `stat(normalizedName)` may synthesize directory `FileInfo`, allowing object-store prefixes to appear as WebDAV directories. |
| `WithRecursiveMkdir()` | `Mkdir` creates exactly one path segment and fails when parents are missing. | `Mkdir` delegates to `MkdirAll`, so WebDAV `MKCOL` can create missing parent prefixes. |

Example object-store-oriented setup:

```go
handler := &webdav.Handler{
    FileSystem: aferodav.NewFS(
        objectFS,
        aferodav.WithPathCleaner(strictCleanPath),
        aferodav.WithObjectStoreWriteMode(),
        aferodav.WithSyntheticWriteStat(),
        aferodav.WithImplicitDirectoryStat(statImplicitPrefix),
        aferodav.WithRecursiveMkdir(),
    ),
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
