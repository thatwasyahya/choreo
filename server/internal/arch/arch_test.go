// Package arch holds architecture tests: rules about the shape of the code that
// a reviewer would otherwise have to remember forever.
//
// Both tests here are required by docs/SYNC_PROTOCOL.md, section "Isolation
// entre foyers". Household isolation is a security boundary, and the protocol
// deliberately does NOT use row-level security — a decorative second belt is
// worse than no second belt, because the first one gets loosened. What replaces
// it is mechanically checkable, and this is where it gets checked.
package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// moduleRoot is the server module, two levels above this package.
const moduleRoot = "../.."

// storeDir is the one place allowed to talk SQL.
const storeDir = "internal/store"

// sqlStart matches a string literal that BEGINS with a SQL statement. Anchoring
// to the start is what keeps prose out: a log message saying "update the cursor"
// is not a query, and flagging it would teach people to ignore this test.
var sqlStart = regexp.MustCompile(`(?is)^(select|insert\s+into|update|delete\s+from|with)\s`)

// TestNoSQLOutsideStore fails if a SQL query is written anywhere but the store.
//
// The point is not tidiness. Every query has to carry household_id, and that is
// only reviewable if every query lives in one directory.
func TestNoSQLOutsideStore(t *testing.T) {
	offenders := make([]string, 0)

	forEachGoFile(t, func(path string, file *ast.File, fset *token.FileSet) {
		rel := relative(t, path)
		if strings.HasPrefix(rel, storeDir) {
			return
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}

			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				// A raw string with an odd escape is not our business.
				return true
			}

			if sqlStart.MatchString(strings.TrimLeft(value, " \t\r\n")) {
				pos := fset.Position(lit.Pos())
				offenders = append(offenders,
					rel+":"+strconv.Itoa(pos.Line)+" — "+firstLine(value))
			}
			return true
		})
	})

	if len(offenders) > 0 {
		t.Errorf("SQL found outside %s/ — every query must carry household_id, "+
			"and that is only reviewable if they all live in one place:\n  %s",
			storeDir, strings.Join(offenders, "\n  "))
	}
}

// TestStoreMethodsTakeHouseholdID fails if an exported store method does not
// take householdID right after the context.
//
// household_id must be read from the membership on every request, never from the
// request body. Making it the first real argument of every store method means a
// caller cannot forget it: the compiler asks for it.
func TestStoreMethodsTakeHouseholdID(t *testing.T) {
	// Infrastructure, not data access: these legitimately have no household.
	exempt := map[string]bool{
		"Ping":    true,
		"Close":   true,
		"Migrate": true,
		"Open":    true,
	}

	offenders := make([]string, 0)

	forEachGoFile(t, func(path string, file *ast.File, fset *token.FileSet) {
		if !strings.HasPrefix(relative(t, path), storeDir) {
			return
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if exempt[fn.Name.Name] {
				continue
			}

			params := flatten(fn.Type.Params)
			pos := fset.Position(fn.Pos())
			where := relative(t, path) + ":" + strconv.Itoa(pos.Line) + " " + fn.Name.Name

			switch {
			case len(params) < 2:
				offenders = append(offenders, where+" — takes fewer than (ctx, householdID)")
			case params[0] != "ctx":
				offenders = append(offenders, where+" — first parameter is not ctx")
			case params[1] != "householdID":
				offenders = append(offenders,
					where+" — second parameter is "+params[1]+", want householdID")
			}
		}
	})

	if len(offenders) > 0 {
		t.Errorf("store methods must be (ctx, householdID, …) so a caller cannot "+
			"forget the tenant:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// flatten returns parameter names, expanding grouped parameters such as
// (a, b string) into two entries.
func flatten(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}

	names := make([]string, 0, len(fields.List))
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			names = append(names, "_") // unnamed parameter
			continue
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func forEachGoFile(t *testing.T, visit func(string, *ast.File, *token.FileSet)) {
	t.Helper()

	fset := token.NewFileSet()

	err := filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Generated and vendored trees are nobody's architecture.
			if name := entry.Name(); name == "bin" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		visit(path, file, fset)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", moduleRoot, err)
	}
}

func relative(t *testing.T, path string) string {
	t.Helper()

	rel, err := filepath.Rel(moduleRoot, path)
	if err != nil {
		t.Fatalf("relativising %s: %v", path, err)
	}
	return filepath.ToSlash(rel)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 70 {
		s = s[:70] + "…"
	}
	return s
}
