// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package pattern reads a route pattern into its parts: literal text, :name
// parameters and the * wildcard. It needs no router and no server.
package pattern

// Kind says what one part of a route pattern matches. The zero value is Static.
type Kind uint8

const (
	Static Kind = iota // literal text
	Param              // one path segment, written :name
	Any                // the rest of the path, written *
)

// Part is one piece of a pattern: the literal text, the parameter's name, or
// the wildcard's text. A backslash before a colon makes the colon static. After
// a parameter name it starts an inline verb (`/:name\:cancel`) when the rest of
// that path segment is static.
type Part struct {
	Kind  Kind
	Value string
}

// Parse returns the parts of path. An empty path has none.
func Parse(path string) []Part {
	var parts []Part
	Walk(path, func(part Part) { parts = append(parts, part) })
	return parts
}

// Walk calls emit for each part of path in order. It allocates nothing itself:
// it is the common syntax scanner, so a caller that builds a URL from a pattern
// can read the parts without a slice of them. Parse collects what Walk emits.
func Walk(path string, emit func(Part)) {
	for i := 0; i < len(path); {
		if isEscapedColon(path, i) {
			emit(Part{Kind: Static, Value: ":"})
			i += 2
		} else if path[i] == ':' {
			start := i + 1
			i = start
			plainName := true // an escaped colon only starts an inline verb after a name without ':' or '*'
			for i < len(path) && path[i] != '/' {
				if isEscapedColon(path, i) {
					if plainName && isInlineVerb(path[i+2:]) {
						break
					}
					// not an inline verb: the rest of the segment is the param name, as before inline verbs
					for i < len(path) && path[i] != '/' {
						i++
					}
					break
				}
				if path[i] == ':' || path[i] == '*' {
					plainName = false
				}
				i++
			}
			emit(Part{Kind: Param, Value: path[start:i]})
		} else if path[i] == '*' {
			start := i
			for i < len(path) && path[i] != '/' {
				i++
			}
			emit(Part{Kind: Any, Value: path[start:i]})
		} else {
			start := i
			for i < len(path) && path[i] != ':' && path[i] != '*' && !isEscapedColon(path, i) {
				i++
			}
			emit(Part{Kind: Static, Value: path[start:i]})
		}
	}
}

func isEscapedColon(path string, i int) bool {
	return path[i] == '\\' && i+1 < len(path) && path[i+1] == ':'
}

// isInlineVerb reports whether the route text after an escaped colon stays
// static up to the end of its path segment. Only then can the router find where
// the parameter value ends by trying the colons in the requested segment. Other
// escaped colons keep the older meaning and remain part of the parameter name.
func isInlineVerb(rest string) bool {
	for i := 0; i < len(rest) && rest[i] != '/'; i++ {
		switch rest[i] {
		case '*':
			return false
		case ':':
			if i == 0 || rest[i-1] != '\\' {
				return false
			}
		}
	}
	return true
}
