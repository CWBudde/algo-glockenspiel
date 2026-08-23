package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNoExportedInternalTypes is the standing form of the Phase 6.1 audit.
//
// This package is public and a separate module builds against it, so an
// exported signature may only mention types a consumer of that module can name.
// A reference to internal/... compiles perfectly here and fails there, which is
// how Bar.BankOscillators shipped returning an oscbank.Oscillator that no
// external caller could declare a variable of: the accessor was callable, but
// its result could not be stored, passed or ranged into a typed variable.
//
// Nothing inside this module can catch that by using the API, because
// same-module code is allowed to import internal/... . So the check is on the
// declarations themselves.
//
// Type aliases are the deliberate exception. Re-exporting through an alias is
// precisely how such a leak is fixed -- the alias makes the type nameable under
// this package's path -- so an alias may name an internal type, provided the
// alias itself is exported. A defined type would not do: it would need a
// conversion at every boundary.
func TestNoExportedInternalTypes(t *testing.T) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	// Every .go file is parsed, build tags included: a constrained file such as
	// cheby_avx2_amd64.go is part of the surface on the platform it builds for,
	// and a leak that only appears under one GOARCH is still a leak.
	checked := 0

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		checked++

		internalNames := internalImportNames(file)
		if len(internalNames) == 0 {
			continue
		}

		checkFile(t, fset, file, internalNames)
	}

	if checked == 0 {
		t.Fatal("no package files parsed; the check would pass vacuously")
	}
}

// internalImportNames maps the local name of every internal import in file to
// its path. A file importing nothing internal cannot leak and is skipped.
func internalImportNames(file *ast.File) map[string]string {
	names := make(map[string]string)

	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.Contains(path, "/internal/") {
			continue
		}

		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}

		names[name] = path
	}

	return names
}

func checkFile(t *testing.T, fset *token.FileSet, file *ast.File, internalNames map[string]string) {
	t.Helper()

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !exportedFunc(d) {
				continue
			}

			checkExpr(t, fset, internalNames, "func "+d.Name.Name, d.Type)

		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}

			for _, spec := range d.Specs {
				checkTypeSpec(t, fset, internalNames, spec.(*ast.TypeSpec))
			}
		}
	}
}

// exportedFunc reports whether decl is reachable from outside the package: an
// exported function, or a method whose receiver type is exported. An exported
// method on an unexported type is unreachable and cannot leak.
func exportedFunc(decl *ast.FuncDecl) bool {
	if !decl.Name.IsExported() {
		return false
	}

	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return true
	}

	return receiverIsExported(decl.Recv.List[0].Type)
}

func receiverIsExported(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	if index, ok := expr.(*ast.IndexExpr); ok { // generic receiver
		expr = index.X
	}

	ident, ok := expr.(*ast.Ident)

	return ok && ident.IsExported()
}

func checkTypeSpec(t *testing.T, fset *token.FileSet, internalNames map[string]string, spec *ast.TypeSpec) {
	t.Helper()

	if !spec.Name.IsExported() {
		return
	}

	// An exported alias may name an internal type: that is the re-export that
	// makes the type usable outside the module.
	if spec.Assign.IsValid() {
		return
	}

	checkExpr(t, fset, internalNames, "type "+spec.Name.Name, spec.Type)
}

// checkExpr walks a type expression and fails on any qualified identifier whose
// package is internal. Unexported struct fields are skipped: they are storage,
// not surface.
func checkExpr(t *testing.T, fset *token.FileSet, internalNames map[string]string, what string, expr ast.Expr) {
	t.Helper()

	if structType, ok := expr.(*ast.StructType); ok {
		for _, field := range structType.Fields.List {
			if !anyNameExported(field.Names) {
				continue
			}

			checkExpr(t, fset, internalNames, what, field.Type)
		}

		return
	}

	ast.Inspect(expr, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		if path, internal := internalNames[ident.Name]; internal {
			t.Errorf("%s at %s exposes %s.%s from %s; an external consumer cannot name that type. "+
				"Re-export it with an exported alias in this package instead.",
				what, fset.Position(sel.Pos()), ident.Name, sel.Sel.Name, path)
		}

		return true
	})
}

func anyNameExported(names []*ast.Ident) bool {
	// An embedded field has no name and is exported if its type is.
	if len(names) == 0 {
		return true
	}

	for _, name := range names {
		if name.IsExported() {
			return true
		}
	}

	return false
}
