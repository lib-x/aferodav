package aferodav

import "path"

// cleanPath normalizes a name to absolute slash-separated WebDAV form.
func cleanPath(name string) string {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}
	return path.Clean(name)
}
