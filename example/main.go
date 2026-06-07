// Example showing both adapter directions.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/lib-x/aferodav"
	"github.com/spf13/afero"
	"golang.org/x/net/webdav"
)

func main() {
	var (
		addr = flag.String("addr", ":8080", "listen address")
		mode = flag.String("mode", "afero-to-webdav", "afero-to-webdav | webdav-to-afero")
	)
	flag.Parse()

	var handler http.Handler

	switch *mode {
	case "afero-to-webdav":
		// Direction 1: afero.Fs -> webdav.FileSystem.
		// Use any afero backend (MemMapFs, OsFs, BasePathFs, your R2Fs, ...)
		// and serve it over WebDAV.
		afs := afero.NewMemMapFs() // swap in any afero.Fs here
		handler = &webdav.Handler{
			FileSystem: aferodav.NewFS(afs),
			LockSystem: webdav.NewMemLS(),
			Logger: func(r *http.Request, err error) {
				if err != nil {
					log.Printf("[webdav] %s %s -> %v", r.Method, r.URL.Path, err)
				}
			},
		}
		log.Println("mode: afero -> webdav (serving MemMapFs over WebDAV)")

	case "webdav-to-afero":
		// Direction 2: webdav.FileSystem -> afero.Fs.
		// Wrap any webdav.FileSystem as an afero.Fs so afero-aware libraries
		// (hugo, viper, ...) can use it transparently.
		wdfs := webdav.NewMemFS() // swap in any webdav.FileSystem here
		afs := aferodav.New(wdfs, context.Background())

		// Demonstrate afero usage: write a file through the adapter.
		f, err := afs.Create("/hello.txt")
		if err != nil {
			log.Fatal(err)
		}
		if _, err := f.WriteString("hello from webdav-backed afero.Fs\n"); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}

		data, err := afero.ReadFile(afs, "/hello.txt")
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("mode: webdav -> afero | read back: %q", data)

		// Then serve it over HTTP via afero's built-in http.FileSystem adapter.
		handler = http.FileServer(afero.NewHttpFs(afs))

	default:
		log.Fatalf("unknown mode: %q", *mode)
	}

	log.Printf("listening on %s", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}
