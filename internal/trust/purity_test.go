// SPDX-License-Identifier: MIT

package trust_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowed is every import a non-test file of this package may have: nothing of the module, so the root can
// forward to this package and this package never reaches back.
var allowed = map[string]bool{"net": true, "net/http": true, "strings": true}

func checkImports(filename string, src any) error {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return err
		}
		if !allowed[path] {
			return fmt.Errorf("%s imports %s, which is outside the allowlist of the trust package", filename, path)
		}
	}
	return nil
}

func TestImportsStayPure(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		if err := checkImports(name, nil); err != nil {
			t.Error(err)
		}
	}
	if checked == 0 {
		t.Fatal("no source file was checked")
	}
}

func TestImportsStayPureViolation(t *testing.T) {
	if checkImports("planted.go", "package planted\n\nimport \"github.com/fiberfx/mx-echo/v7\"\n") == nil {
		t.Fatal("an import of the root passed the check")
	}
}
