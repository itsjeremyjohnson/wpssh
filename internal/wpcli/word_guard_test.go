package wpcli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// wordConversionAllowed lists the files that may convert a value to Word.
// raw passes the user's argv through as the wp command line by design and
// checks it with checkRawArgs.
var wordConversionAllowed = map[string]bool{
	"internal/cmd/raw.go": true,
}

// TestNoValueBecomesWord fails when non-test code converts a value to Word
// or []Word. Build writes words unescaped, so a converted user value reaches
// the remote shell as shell syntax. Pass user values with Arg; Word's type
// already rejects them without a conversion.
func TestNoValueBecomesWord(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		inWPCLI := file.Name.Name == "wpcli"
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 || !isWordType(call.Fun, inWPCLI) {
				return true
			}
			if _, literal := call.Args[0].(*ast.BasicLit); literal || wordConversionAllowed[rel] {
				return true
			}
			t.Errorf("%s: converts a value to wpcli.Word; pass user values with Arg", fset.Position(call.Pos()))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// isWordType reports whether expr names Word or []Word, qualified as
// wpcli.Word outside package wpcli.
func isWordType(expr ast.Expr, inWPCLI bool) bool {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return isWordType(paren.X, inWPCLI)
	}
	if arr, ok := expr.(*ast.ArrayType); ok {
		return isWordType(arr.Elt, inWPCLI)
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return inWPCLI && e.Name == "Word"
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		return ok && pkg.Name == "wpcli" && e.Sel.Name == "Word"
	}
	return false
}
