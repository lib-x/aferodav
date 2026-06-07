package aferodav

import "path"

// cleanPath normalizes a name to absolute slash-separated WebDAV form.
func cleanPath(name string) string {
	if name == "" || name[0] != '/' {
		name = "/" + name
	}
	return path.Clean(name)
}

func cleanPathWithCleaner(op, name string, cleaner PathCleaner) (string, error) {
	if cleaner != nil {
		cleaned, err := cleaner(op, name)
		if err != nil {
			return "", err
		}
		name = cleaned
	}
	return cleanPath(name), nil
}

func fileInfoName(name string) string {
	p := cleanPath(name)
	if p == "/" {
		return "/"
	}
	return path.Base(p)
}
