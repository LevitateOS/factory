// Command unitban is a go vet analyzer that bans unit tests.
//
// Unit testing is bureaucracy (AGENTS.md): test entry points (Test, Fuzz,
// Example, TestMain) may only exist in the integration-test files listed in
// tools/unitban/allow-go.txt. All other packages are production code.
//
// Self-test fixtures live in tools/unitban/testfixtures/ and are skipped by
// the analyzer; scripts/check-go-unitban.sh exercises the analyzer against a
// copy of the fixture outside the skipped directory.
package main

import (
	_ "embed"
	"flag"
	"go/ast"
	"go/types"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

//go:embed allow-go.txt
var embeddedAllow string

var extraAllow = flag.String("allow", "", "comma-separated extra allowlisted file paths (self-test override)")

var (
	testName = regexp.MustCompile(`^Test($|[^a-z])`)
	fuzzName = regexp.MustCompile(`^Fuzz($|[^a-z])`)
)

var analyzer = &analysis.Analyzer{
	Name: "unitban",
	Doc:  "unit tests are banned; test entry points only in allowlisted integration files",
	Run:  run,
}

func allowed(path string, allow map[string]bool) bool {
	for entry := range allow {
		if path == entry || strings.HasSuffix(path, "/"+entry) {
			return true
		}
	}
	return false
}

// testingParam reports whether fn's first parameter has the type ptrName
// from the testing package (e.g. *testing.T), resolved through type info so
// renames, aliases and dot imports are honored.
func testingParam(pass *analysis.Pass, fn *ast.FuncDecl, ptrName string) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	typ := pass.TypesInfo.TypeOf(fn.Type.Params.List[0].Type)
	ptr, ok := typ.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.Obj().Name() != ptrName {
		return false
	}
	pkg := named.Obj().Pkg()
	return pkg != nil && pkg.Path() == "testing"
}

func run(pass *analysis.Pass) (any, error) {
	allow := map[string]bool{}
	for line := range strings.Lines(strings.TrimSpace(embeddedAllow)) {
		if p := strings.TrimSpace(line); p != "" && !strings.HasPrefix(p, "#") {
			allow[p] = true
		}
	}
	for p := range strings.SplitSeq(*extraAllow, ",") {
		if p = strings.TrimSpace(p); p != "" {
			allow[p] = true
		}
	}
	for _, file := range pass.Files {
		pos := pass.Fset.Position(file.Package)
		if strings.Contains(pos.Filename, "/testfixtures/") {
			continue
		}
		if allowed(pos.Filename, allow) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			banned := name == "TestMain" ||
				strings.HasPrefix(name, "Example") ||
				(testName.MatchString(name) && testingParam(pass, fn, "T")) ||
				(fuzzName.MatchString(name) && testingParam(pass, fn, "F"))
			if banned {
				pass.Reportf(fn.Pos(), "unit tests are banned (AGENTS.md): %s; see docs/development/why-no-unit-tests.md; integration tests live in allowlisted files (tools/unitban/allow-go.txt)", name)
			}
		}
	}
	return nil, nil
}

func main() { singlechecker.Main(analyzer) }
