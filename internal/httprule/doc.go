// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package httprule holds decisions about an HTTP request as functions of strings: where to redirect, whether to
// compress, whether a path is safe to resolve as a file, and whether an origin is allowed.
//
// The functions read no request, write no response and import only strings, so a caller on any router can ask the
// same question the middleware package asks.
package httprule
