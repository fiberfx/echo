// SPDX-License-Identifier: MIT

package bindengine_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowed is every import a non-test file of this package may have: the standard library packages engine.go
// uses, and nothing of the module's root.
var allowed = map[string]bool{
	"encoding":       true,
	"errors":         true,
	"fmt":            true,
	"mime/multipart": true,
	"reflect":        true,
	"strconv":        true,
	"strings":        true,
	"sync":           true,
	"time":           true,
}

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
			return fmt.Errorf("%s imports %s, which is outside the allowlist of the bind engine", filename, path)
		}
	}
	return nil
}

func TestImportsStayInAllowlist(t *testing.T) {
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

func TestImportsNoRoot(t *testing.T) {
	if checkImports("planted.go", "package planted\n\nimport \"github.com/fiberfx/mx-echo/v7\"\n") == nil {
		t.Fatal("an import of the root passed the check")
	}
}
