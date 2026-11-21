// SPDX-License-Identifier: MIT

package mcprule_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// allowed is every import a non-test file of this package may have.
var allowed = map[string]bool{"encoding/base64": true, "slices": true, "strconv": true, "strings": true, "unicode/utf8": true}

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
			return fmt.Errorf("%s imports %s, which is outside the allowlist of a pure package", filename, path)
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
	for _, path := range []string{"os", "net", "net/http", "time", "fmt", "encoding/json", "log", "log/slog"} {
		src := "package planted\n\nimport \"" + path + "\"\n"
		if checkImports("planted.go", src) == nil {
			t.Errorf("an import of %s passed the check", path)
		}
	}
}
