// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// Package httprule holds decisions about an HTTP request as functions of strings: where to redirect, whether to
// compress, whether a path is safe to resolve as a file, and whether an origin is allowed.
//
// The functions read no request, write no response and import only strings, so the middleware package and the root's
// static file handler ask the same question of one copy.
//
// The package is internal: it has no caller outside the middleware package and the root, and v6.1.0 never exported
// these decisions, so keeping them out of the public API breaks no one and leaves them free to change.
package httprule
