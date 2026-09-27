package main_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A function whose ONLY reference is its own declaration is not a feature: it
// is a producer with no consumer, and no unit test will ever say so, because
// the test calls it directly. Ported from the conductor repo, where three of
// these shipped in one session behind passing tests.
//
// Scope is deliberately narrow so the gate does not cry wolf:
//   - plain functions only, never methods (a method can be reached through an
//     interface this cannot see);
//   - a reference from a _test.go file does not count ("only its own test
//     calls it" is exactly the condition being detected);
//   - main and init are entry points.
//
// To add a genuine exception, put it in allowedUnused WITH A REASON. Every
// entry is debt; the gate's job is that the list does not grow.
var allowedUnused = map[string]string{}

func TestNoFunctionIsCalledOnlyByItsOwnTest(t *testing.T) {
	t.Parallel()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	declaredIn := map[string]string{}
	refs := map[string]int{}
	fset := token.NewFileSet()

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "vendor", "node_modules", "testdata", ".golangci-cache":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil || f == nil {
			return nil //nolint:nilerr // deliberate: an unparseable file is the compiler's problem, not this gate's
		}
		rel, _ := filepath.Rel(root, path)

		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name == "main" || fn.Name.Name == "init" {
				continue
			}
			declaredIn[fn.Name.Name] = rel
		}

		// Count identifier uses, skipping each function's own name node so a
		// declaration never counts as its own caller.
		count := func(n ast.Node) bool {
			if id, isID := n.(*ast.Ident); isID {
				refs[id.Name]++
			}
			return true
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return count(n)
			}
			if fn.Type != nil {
				ast.Inspect(fn.Type, count)
			}
			if fn.Body != nil {
				ast.Inspect(fn.Body, count)
			}
			return false
		})
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}

	var orphans []string
	for name, file := range declaredIn {
		if _, waived := allowedUnused[name]; waived {
			continue
		}
		if refs[name] == 0 {
			orphans = append(orphans, name+"  ("+file+")")
		}
	}
	sort.Strings(orphans)

	if len(orphans) > 0 {
		t.Errorf("%d function(s) have no caller outside their own tests; a producer with no consumer is not done.\n"+
			"Wire each into the path that should call it, delete it, or add it to allowedUnused WITH A REASON:\n  %s",
			len(orphans), strings.Join(orphans, "\n  "))
	}
}
