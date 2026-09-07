package threat

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Problem is one thing wrong with the matrix.
type Problem struct {
	// RowID is the row it is about, or "" for a problem with the matrix as a
	// whole.
	RowID string `json:"row_id,omitempty"`
	What  string `json:"what"`
}

func (p Problem) String() string {
	if p.RowID == "" {
		return p.What
	}
	return p.RowID + ": " + p.What
}

// Check reads the matrix against the repository at root and reports everything
// wrong with it.
//
// Four things are checked, and each one is a way a threat model rots:
//
//  1. **Structure.** A duplicate id, a row with no category, no mitigation, or
//     no evidence of any kind.
//  2. **The evidence exists.** Every Evidence entry names a package directory
//     that is there and a `func Test…` that is in it. This is the check that
//     makes the matrix survive a refactor: rename a test and the model that
//     cited it fails, instead of quietly asserting something nothing proves.
//  3. **Every category has a row.** The categories are the enumeration the
//     hardening work was scoped by; one with nothing in it is a gap somebody
//     needs to see.
//  4. **Residual risks are owned and dated.** No owner means nobody accepted
//     it. A review date that has passed means nobody has looked at it since,
//     which is the whole point of writing one down.
func Check(root string, now time.Time) []Problem {
	var problems []Problem

	tests, err := testsIn(root)
	if err != nil {
		return []Problem{{What: err.Error()}}
	}

	seen := map[string]bool{}
	covered := map[Category]bool{}
	for _, row := range rows {
		if seen[row.ID] {
			problems = append(problems, Problem{row.ID, "the id is used twice"})
		}
		seen[row.ID] = true
		covered[row.Category] = true

		for field, value := range map[string]string{
			"a title": row.Title, "an asset": row.Asset,
			"a vector": row.Vector, "a mitigation": row.Mitigation,
		} {
			if strings.TrimSpace(value) == "" {
				problems = append(problems, Problem{row.ID, "the row has no " + field})
			}
		}
		if !knownCategory(row.Category) {
			problems = append(problems, Problem{row.ID,
				fmt.Sprintf("category %q is not one of the declared ones", row.Category)})
		}
		if !row.Covered() {
			problems = append(problems, Problem{row.ID,
				"the row has neither an automated test nor a manual procedure. " +
					"A threat with no evidence is a claim, and this file is not for claims"})
		}
		for _, evidence := range row.Evidence {
			names, ok := tests[evidence.Package]
			if !ok {
				problems = append(problems, Problem{row.ID,
					fmt.Sprintf("evidence names package %q, which has no test files in this repository",
						evidence.Package)})
				continue
			}
			if !names[evidence.Test] {
				problems = append(problems, Problem{row.ID,
					fmt.Sprintf("evidence names %s.%s, which is not a test in this repository. "+
						"If it was renamed, rename it here too; if it was deleted, this row has lost its proof",
						evidence.Package, evidence.Test)})
			}
		}

		if row.Residual == nil {
			continue
		}
		if strings.TrimSpace(row.Residual.What) == "" || strings.TrimSpace(row.Residual.Why) == "" {
			problems = append(problems, Problem{row.ID, "the residual risk says neither what nor why"})
		}
		if strings.TrimSpace(row.Residual.Owner) == "" {
			problems = append(problems, Problem{row.ID,
				"the residual risk has no owner. A risk nobody is named for is a risk nobody accepted"})
		}
		review, err := time.Parse("2006-01-02", row.Residual.Review)
		switch {
		case err != nil:
			problems = append(problems, Problem{row.ID,
				fmt.Sprintf("the residual risk's review date %q is not a YYYY-MM-DD date", row.Residual.Review)})
		case !review.After(now.UTC()):
			problems = append(problems, Problem{row.ID,
				fmt.Sprintf("the residual risk's review date (%s) has passed. "+
					"This is the mechanism, not a defect: look at it, decide again, and move the date or fix the risk",
					row.Residual.Review)})
		}
	}

	for _, category := range Categories {
		if !covered[category] {
			problems = append(problems, Problem{What: fmt.Sprintf("no row is in category %q", category)})
		}
	}
	sort.Slice(problems, func(i, j int) bool {
		if problems[i].RowID != problems[j].RowID {
			return problems[i].RowID < problems[j].RowID
		}
		return problems[i].What < problems[j].What
	})
	return problems
}

func knownCategory(category Category) bool {
	for _, known := range Categories {
		if known == category {
			return true
		}
	}
	return false
}

// testsIn collects every `func Test…` in the repository, by package directory
// relative to root.
//
// The source is parsed rather than grepped because a name inside a comment or a
// string is not a test, and a matrix that could be satisfied by a comment would
// be satisfiable by writing one.
func testsIn(root string) (map[string]map[string]bool, error) {
	found := map[string]map[string]bool{}
	fileSet := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "graft", "dist", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			return fmt.Errorf("threat: parsing %s: %w", path, err)
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		key := filepath.ToSlash(relative)
		if found[key] == nil {
			found[key] = map[string]bool{}
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			if strings.HasPrefix(function.Name.Name, "Test") {
				found[key][function.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}
