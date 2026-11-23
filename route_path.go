// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"strings"

	"github.com/fiberfx/mx-echo/v7/route/pattern"
)

// routeTreePath writes the parts of a route pattern as the router tree stores
// them. The pattern grammar itself is in package route/pattern.
func routeTreePath(parts []pattern.Part) (string, []int) {
	var path strings.Builder
	var paramMarkers []int
	for _, part := range parts {
		switch part.Kind {
		case pattern.Static:
			path.WriteString(part.Value)
		case pattern.Param:
			paramMarkers = append(paramMarkers, path.Len())
			path.WriteByte(paramLabel)
		case pattern.Any:
			path.WriteByte(anyLabel)
			return path.String(), paramMarkers
		}
	}
	return path.String(), paramMarkers
}
