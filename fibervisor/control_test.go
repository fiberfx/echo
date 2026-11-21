// SPDX-License-Identifier: MIT

package fibervisor_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// controlsProcess reports whether the function fn of the package path ends the process or takes its signals.
func controlsProcess(path, fn string) bool {
	switch path {
	case "os":
		return fn == "Exit"
	case "os/signal":
		return fn == "Notify" || fn == "NotifyContext"
	case "log":
		return strings.HasPrefix(fn, "Fatal")
	}
	return false
}

// processControl returns the calls in src, the Go source of the file name, that end the process or take its
// signals, each as its position and the call. A package is known by its import path, whatever name the file
// gives it.
func processControl(name string, src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{} // the name the file gives an import, and the import's path
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		paths[local] = path
	}
	var calls []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && controlsProcess(paths[pkg.Name], sel.Sel.Name) {
			calls = append(calls, fmt.Sprintf("%s: %s.%s", fset.Position(call.Pos()), pkg.Name, sel.Sel.Name))
		}
		return true
	})
	return calls, nil
}

// TestNoProcessControl holds the package to its word that it starts nothing and stops nothing: ending the process
// and taking its signals are the service's main's, with StopSignals naming what to take.
func TestNoProcessControl(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var checked []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		calls, err := processControl(name, src)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			t.Errorf("%s: the package ends the process or takes its signals", call)
		}
		checked = append(checked, name)
	}
	if !slices.Contains(checked, "fibervisor.go") || !slices.Contains(checked, "supervision.go") {
		t.Fatalf("checked %v, which misses the package's own files", checked)
	}
}

func TestNoProcessControlViolation(t *testing.T) {
	const clean = `package fibervisor

import (
	"log"
	"os"
)

func read() string {
	log.Print("reading")
	return os.Getenv("FV_PATH")
}
`
	if calls, err := processControl("clean.go", []byte(clean)); err != nil || len(calls) != 0 {
		t.Fatalf("a file that ends nothing and takes no signal gave %v, %v", calls, err)
	}
	for _, plant := range []struct{ name, src string }{
		{"signal.Notify", `package fibervisor

import (
	"os"
	"os/signal"
)

func stopOn(c chan os.Signal) { signal.Notify(c, StopSignals()...) }
`},
		{"signal.NotifyContext under another name", `package fibervisor

import (
	"context"

	sig "os/signal"
)

func stopping() (context.Context, context.CancelFunc) {
	return sig.NotifyContext(context.Background(), StopSignals()...)
}
`},
		{"os.Exit", `package fibervisor

import "os"

func exit() { os.Exit(1) }
`},
		{"log.Fatalf", `package fibervisor

import "log"

func fail(err error) { log.Fatalf("fv: %v", err) }
`},
	} {
		calls, err := processControl(plant.name+".go", []byte(plant.src))
		if err != nil {
			t.Fatal(err)
		}
		if len(calls) != 1 {
			t.Errorf("the planted %s gave %v, want the one call", plant.name, calls)
		}
	}
}
