// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package httprule

import "strings"

// HasDotOrEmptySegment decides whether URL path p must not be resolved as a file: whether it has a ".", ".." or empty
// segment, or a segment with a "." or ".." part between backslashes (e.g. `..\x`). A single leading and a single
// trailing slash are allowed.
func HasDotOrEmptySegment(p string) bool {
	if strings.HasPrefix(p, "//") {
		return true
	}
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return false
	}
	for segment := range strings.SplitSeq(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return true
		}
		// A backslash is a literal character in fs.FS names, but a filesystem that wrongly treats it as a separator
		// (for example one built on filepath.Join on Windows) would resolve `..\` outside its root.
		if strings.Contains(segment, `\`) {
			for part := range strings.SplitSeq(segment, `\`) {
				if part == "." || part == ".." {
					return true
				}
			}
		}
	}
	return false
}

// HasUncleanWildcardPath decides whether p, the part of a path that a wildcard route matched, must not be resolved
// as a file. It accounts for the slash already consumed by a route ending in /*: a leading slash in that wildcard is
// an empty segment, while a prefix such as /assets* may legitimately capture /file.
func HasUncleanWildcardPath(p, routePath string) bool {
	return (strings.HasSuffix(routePath, "/*") && strings.HasPrefix(p, "/")) || HasDotOrEmptySegment(p)
}
