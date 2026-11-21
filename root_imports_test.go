// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This file is row FC-15: the root's non-test files import the standard library and the module's own pure
// and serving packages, and nothing of the fv join. The join is opt-in: a service imports fibervisor or
// observe/stdhttp, the root never does, so the root also never reads fv's environment.

const rootImportsModule = "github.com/fiberfx/mx-echo/v6/"

// rootImportsAllowed holds the module's packages the root may import.
var rootImportsAllowed = map[string]bool{
	rootImportsModule + "bind":              true,
	rootImportsModule + "server":            true,
	rootImportsModule + "route/pattern":     true,
	rootImportsModule + "outcome":           true,
	rootImportsModule + "reqid":             true,
	rootImportsModule + "observe":           true,
	rootImportsModule + "internal/httprule": true,
}

// rootImportsRefused names the packages of the join, so that an import of one says why it is refused.
var rootImportsRefused = map[string]bool{
	rootImportsModule + "fibervisor":      true,
	rootImportsModule + "observe/stdhttp": true,
}

// rootImportsText holds the text no root source may hold: the names of the variables fv gives a leaf.
var rootImportsText = []string{"FV_", "TRACEPARENT"}

// checkRootImports returns an error when src, the source of the root file filename, imports a path outside
// the standard library and the allowlist, or holds the text of fv's environment.
func checkRootImports(filename string, src []byte) error {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return err
		}
		switch first, _, _ := strings.Cut(path, "/"); {
		case rootImportsRefused[path]:
			return fmt.Errorf("%s imports %s: the join is opt-in, a service imports it and the root never does", filename, path)
		case rootImportsAllowed[path], !strings.Contains(first, "."):
		default:
			return fmt.Errorf("%s imports %s, which is neither the standard library nor in the root's allowlist", filename, path)
		}
	}
	for _, text := range rootImportsText {
		if bytes.Contains(src, []byte(text)) {
			return fmt.Errorf("%s holds the text %s: the root does not read fv's environment", filename, text)
		}
	}
	return nil
}

func TestRootImports(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if err := checkRootImports(name, src); err != nil {
			t.Error(err)
		}
	}
	if checked == 0 {
		t.Fatal("no root source file was checked")
	}
}

func TestRootImportsViolation(t *testing.T) {
	planted := []struct {
		name string
		src  string
	}{
		{"an import of fibervisor", "package echo\n\nimport \"github.com/fiberfx/mx-echo/v6/fibervisor\"\n"},
		{"an import of observe/stdhttp", "package echo\n\nimport \"github.com/fiberfx/mx-echo/v6/observe/stdhttp\"\n"},
		{"an import of the bare httprule, which is internal", "package echo\n\nimport \"github.com/fiberfx/mx-echo/v6/httprule\"\n"},
		{"an import outside the module", "package echo\n\nimport \"golang.org/x/net/http2\"\n"},
		{"a read of FV_PATH", "package echo\n\nimport \"os\"\n\nvar attempt = os.Getenv(\"FV_PATH\")\n"},
		{"a read of TRACEPARENT", "package echo\n\nimport \"os\"\n\nvar link = os.Getenv(\"TRACEPARENT\")\n"},
	}
	for _, p := range planted {
		if checkRootImports("planted.go", []byte(p.src)) == nil {
			t.Errorf("%s passed the check", p.name)
		}
	}
	err := checkRootImports("planted.go", []byte(planted[0].src))
	if err == nil || !strings.Contains(err.Error(), "opt-in") {
		t.Errorf("the refusal of fibervisor says %v, want why the root never imports it", err)
	}
	if err := checkRootImports("planted.go", []byte("package echo\n\nimport (\n\t\"net/http\"\n\n\t\"github.com/fiberfx/mx-echo/v6/observe\"\n)\n")); err != nil {
		t.Errorf("a source the root may hold was refused: %v", err)
	}
}
