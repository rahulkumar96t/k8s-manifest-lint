package lint

import (
	"path"
	"strings"
)

// imageRef is a parsed container image reference.
type imageRef struct {
	Registry   string // normalized: Docker Hub becomes docker.io
	Repository string // e.g. library/nginx
	Tag        string
	Digest     string
}

// parseImage splits an image reference the way container runtimes do:
// the first path component is a registry only if it looks like a host
// (contains "." or ":", or is "localhost"); otherwise the image is on
// Docker Hub, and single-component names live under library/.
func parseImage(s string) imageRef {
	var ref imageRef
	name := s
	if i := strings.IndexByte(name, '@'); i >= 0 {
		name, ref.Digest = name[:i], name[i+1:]
	}
	if i := strings.LastIndexByte(name, ':'); i > strings.LastIndexByte(name, '/') {
		name, ref.Tag = name[:i], name[i+1:]
	}
	first, rest, found := strings.Cut(name, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		ref.Registry, ref.Repository = first, rest
	} else {
		ref.Registry, ref.Repository = "docker.io", name
		if !found {
			ref.Repository = "library/" + name
		}
	}
	if ref.Registry == "index.docker.io" || ref.Registry == "registry-1.docker.io" {
		ref.Registry = "docker.io"
	}
	return ref
}

// registryAllowed reports whether ref matches one of the allow-list
// entries. An entry is a host ("artifactory.example.com") or a host/path
// prefix ("artifactory.example.com/docker-local"); each "/"-separated
// segment is a path.Match pattern. Matching is per segment, so
// "artifactory.example.com/team" does not allow
// "artifactory.example.com/team-other".
func registryAllowed(ref imageRef, allowed []string) bool {
	segs := strings.Split(ref.Registry+"/"+ref.Repository, "/")
	for _, entry := range allowed {
		want := strings.Split(strings.Trim(entry, "/"), "/")
		if len(want) > len(segs) {
			continue
		}
		ok := true
		for i, pattern := range want {
			if m, _ := path.Match(pattern, segs[i]); !m {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
