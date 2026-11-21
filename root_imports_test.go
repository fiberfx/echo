// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// This file is row FC-15: the root's non-test files import the standard library and the module's own pure
// and serving packages, and nothing of the fv join. The join is opt-in: a service imports fibervisor or
// observe/stdhttp, the root never does, so the root also never reads fv's environment.
//
// Row R6 holds the same for every package of the module the root imports, directly or through another: none
// imports the join or a module outside the standard library, and none holds the text of fv's environment. A
// body moved from the root into an internal package stays under the rule it had in the root.

const rootImportsModule = "github.com/fiberfx/mx-echo/v6/"

// rootImportsAllowed holds the module's packages the root may import. internal/trust and internal/bindengine are
// listed before they exist (v6.6.1, v6.6.2), so that the cars moving the root's bodies into them pass on their own.
var rootImportsAllowed = map[string]bool{
	rootImportsModule + "bind":                true,
	rootImportsModule + "server":              true,
	rootImportsModule + "route/pattern":       true,
	rootImportsModule + "outcome":             true,
	rootImportsModule + "reqid":               true,
	rootImportsModule + "observe":             true,
	rootImportsModule + "internal/httprule":   true,
	rootImportsModule + "internal/trust":      true,
	rootImportsModule + "internal/bindengine": true,
}

// rootImportsRefused names the packages of the join, so that an import of one says why it is refused.
var rootImportsRefused = map[string]bool{
	rootImportsModule + "fibervisor":      true,
	rootImportsModule + "observe/stdhttp": true,
}

// rootImportsText holds the text no root source may hold: the names of the variables fv gives a leaf.
var rootImportsText = []string{"FV_", "TRACEPARENT"}

// fileImports returns the import paths of src, the source of filename.
func fileImports(filename string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// checkRootImports returns an error when src, the source of the root file filename, imports a path outside
// the standard library and the allowlist, or holds the text of fv's environment.
func checkRootImports(filename string, src []byte) error {
	return checkImports(filename, src, rootImportsAllowed)
}

// checkClosureImports returns an error when src, the source of filename in a package of the module that the
// root imports directly or through another, imports the join or a path outside the standard library and the
// module, or holds the text of fv's environment.
func checkClosureImports(filename string, src []byte) error {
	return checkImports(filename, src, nil)
}

// checkImports applies the rules to one file. A nil allowed admits every package of the module but the join's.
func checkImports(filename string, src []byte, allowed map[string]bool) error {
	paths, err := fileImports(filename, src)
	if err != nil {
		return err
	}
	for _, p := range paths {
		first, _, _ := strings.Cut(p, "/")
		switch {
		case rootImportsRefused[p]:
			return fmt.Errorf("%s imports %s: the join is opt-in, a service imports it and the root never does", filename, p)
		case !strings.Contains(first, "."):
		case allowed == nil && strings.HasPrefix(p, rootImportsModule):
		case allowed[p]:
		case allowed == nil:
			return fmt.Errorf("%s imports %s, which is neither the standard library nor a package of the module", filename, p)
		default:
			return fmt.Errorf("%s imports %s, which is neither the standard library nor in the root's allowlist", filename, p)
		}
	}
	for _, text := range rootImportsText {
		if bytes.Contains(src, []byte(text)) {
			return fmt.Errorf("%s holds the text %s: the root does not read fv's environment", filename, text)
		}
	}
	return nil
}

// checkRootClosure walks fsys, the module's directory, from the root package along every import of the module
// in a non-test file, and checks each file it reads: the root's against the allowlist, every other against the
// closure's rules. The join's packages are refused, not entered. It returns the packages it read, the root
// first as ".", and what it found.
func checkRootClosure(fsys fs.FS) ([]string, []error) {
	var errs []error
	dirs := []string{"."}
	seen := map[string]bool{".": true}
	for i := 0; i < len(dirs); i++ {
		dir := dirs[i]
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("package %s: %w", dir, err))
			continue
		}
		check := checkClosureImports
		if dir == "." {
			check = checkRootImports
		}
		files := 0
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			file := path.Join(dir, name)
			src, err := fs.ReadFile(fsys, file)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			files++
			if err := check(file, src); err != nil {
				errs = append(errs, err)
			}
			imports, err := fileImports(file, src)
			if err != nil {
				continue
			}
			for _, p := range imports {
				sub, ok := strings.CutPrefix(p, rootImportsModule)
				if ok && !rootImportsRefused[p] && !seen[sub] {
					seen[sub] = true
					dirs = append(dirs, sub)
				}
			}
		}
		if files == 0 {
			errs = append(errs, fmt.Errorf("package %s: no non-test Go file to check", dir))
		}
	}
	return dirs, errs
}

func TestRootImports(t *testing.T) {
	dirs, errs := checkRootClosure(os.DirFS("."))
	for _, err := range errs {
		t.Error(err)
	}
	t.Logf("the root and the packages of the module it imports: %s", strings.Join(dirs, ", "))
	if !slices.ContainsFunc(dirs, func(dir string) bool { return strings.HasPrefix(dir, "internal/") }) {
		t.Errorf("the walk reached no internal package (%s): row R6 would be blind to them again", strings.Join(dirs, ", "))
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

// rootClosureModule returns a module in memory: the root imports internal/httprule and observe, observe
// imports internal/clock, and fibervisor, which nothing imports, reads FV_PATH. Each edit replaces or adds one
// file, given as its name and its source.
func rootClosureModule(edits ...[2]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	add := func(name, src string) {
		fsys[name] = &fstest.MapFile{Data: []byte(src)}
	}
	add("echo.go", "package echo\n\nimport (\n\t\"github.com/fiberfx/mx-echo/v6/internal/httprule\"\n\t\"github.com/fiberfx/mx-echo/v6/observe\"\n)\n")
	add("internal/httprule/rule.go", "package httprule\n\nimport \"strings\"\n")
	add("observe/observe.go", "package observe\n\nimport \"github.com/fiberfx/mx-echo/v6/internal/clock\"\n")
	add("internal/clock/clock.go", "package clock\n\nimport \"time\"\n")
	add("fibervisor/fibervisor.go", "package fibervisor\n\nimport \"os\"\n\nvar attempt = os.Getenv(\"FV_PATH\")\n")
	for _, edit := range edits {
		add(edit[0], edit[1])
	}
	return fsys
}

func TestRootImportsClosureViolation(t *testing.T) {
	dirs, errs := checkRootClosure(rootClosureModule(
		[2]string{"internal/httprule/rule_test.go", "package httprule\n\nconst attempt = \"FV_PATH\"\n"},
	))
	if err := errors.Join(errs...); err != nil {
		t.Errorf("the clean module was refused: %v", err)
	}
	if want := []string{".", "internal/httprule", "observe", "internal/clock"}; !slices.Equal(dirs, want) {
		t.Errorf("the walk read %q, want %q", dirs, want)
	}

	planted := []struct {
		name string
		edit [2]string
		want string
	}{
		{"FV_ in an internal package the root imports", [2]string{"internal/httprule/env.go", "package httprule\n\nimport \"os\"\n\nvar attempt = os.Getenv(\"FV_PATH\")\n"}, "internal/httprule/env.go holds the text FV_"},
		{"TRACEPARENT two imports down", [2]string{"internal/clock/link.go", "package clock\n\nconst header = \"TRACEPARENT\"\n"}, "internal/clock/link.go holds the text TRACEPARENT"},
		{"the join imported by a package the root imports", [2]string{"internal/clock/clock.go", "package clock\n\nimport \"github.com/fiberfx/mx-echo/v6/fibervisor\"\n"}, "opt-in"},
		{"a module outside the standard library two imports down", [2]string{"internal/clock/clock.go", "package clock\n\nimport \"golang.org/x/time/rate\"\n"}, "golang.org/x/time/rate, which is neither the standard library nor a package of the module"},
		{"an import of a package the module does not hold", [2]string{"internal/clock/clock.go", "package clock\n\nimport \"github.com/fiberfx/mx-echo/v6/internal/missing\"\n"}, "package internal/missing"},
	}
	for _, p := range planted {
		_, errs := checkRootClosure(rootClosureModule(p.edit))
		err := errors.Join(errs...)
		if err == nil || !strings.Contains(err.Error(), p.want) {
			t.Errorf("%s: the walk found %v, want an error holding %q", p.name, err, p.want)
		}
	}
}
