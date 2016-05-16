package suggest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"path"
	"path/filepath"
	"strings"
)

type Suggester struct {
	debug   bool
	context *build.Context
}

func New(debug bool, context *build.Context) *Suggester {
	return &Suggester{
		debug:   debug,
		context: context,
	}
}

// Suggest returns a list of suggestion candidates and the length of
// the text that should be replaced, if any.
func (c *Suggester) Suggest(filename string, data []byte, cursor int) ([]Candidate, int) {
	if cursor < 0 {
		return nil, 0
	}

	fset, pos, pkg := c.analyzePackage(filename, data, cursor)
	scope := pkg.Scope().Innermost(pos)

	ctx, expr, partial := deduce_cursor_context_helper(data, cursor)
	b := candidateCollector{
		localpkg: pkg,
		partial:  partial,
		filter:   objectFilters[partial],
	}

	switch ctx {
	case importContext:
		c.getImportCandidates(partial, &b)

	case selectContext:
		tv, _ := types.Eval(fset, pkg, pos, expr)
		if tv.IsType() {
			c.typeSelectCandidates(tv.Type, &b)
			break
		}
		if tv.IsValue() {
			c.valueSelectCandidates(tv.Type, tv.Addressable(), &b)
			break
		}

		_, obj := scope.LookupParent(expr, pos)
		if pkgName, isPkg := obj.(*types.PkgName); isPkg {
			c.packageCandidates(pkgName.Imported(), &b)
			break
		}

		return nil, 0

	case compositeLiteralContext:
		tv, _ := types.Eval(fset, pkg, pos, expr)
		if tv.IsType() {
			if _, isStruct := tv.Type.Underlying().(*types.Struct); isStruct {
				c.fieldNameCandidates(tv.Type, &b)
				break
			}
		}

		fallthrough
	default:
		c.scopeCandidates(scope, pos, &b)
	}

	res := b.getCandidates()
	if len(res) == 0 {
		return nil, 0
	}
	return res, len(partial)
}

// Safe to use in new code.
func (c *Suggester) getImportCandidates(partial string, b *candidateCollector) {
	pkgdir := fmt.Sprintf("%s_%s", c.context.GOOS, c.context.GOARCH)
	srcdirs := c.context.SrcDirs()
	for _, srcpath := range srcdirs {
		// convert srcpath to pkgpath and get candidates
		pkgpath := path.Join(path.Dir(filepath.ToSlash(srcpath)), "pkg", pkgdir)
		get_import_candidates_dir(pkgpath, partial, b)
	}
}

func get_import_candidates_dir(root, partial string, b *candidateCollector) {
	var fpath string
	var match bool
	if strings.HasSuffix(partial, "/") {
		fpath = path.Join(root, partial)
	} else {
		fpath = path.Join(root, path.Dir(partial))
		match = true
	}
	fi, err := ioutil.ReadDir(fpath)
	if err != nil {
		panic(err)
	}
	for i := range fi {
		name := fi[i].Name()
		rel, err := filepath.Rel(root, path.Join(fpath, name))
		if err != nil {
			panic(err)
		}
		rel = filepath.ToSlash(rel)
		// TODO(mdempsky): Case-insensitive import path matching?
		if match && !strings.HasPrefix(rel, partial) {
			continue
		} else if fi[i].IsDir() {
			get_import_candidates_dir(root, rel+"/", b)
		} else {
			ext := path.Ext(name)
			if ext != ".a" {
				continue
			} else {
				rel = rel[0 : len(rel)-2]
			}
			b.appendImport(rel)
		}
	}
}

func (c *Suggester) analyzePackage(filename string, data []byte, cursor int) (*token.FileSet, token.Pos, *types.Package) {
	// If we're in trailing white space at the end of a scope,
	// sometimes go/types doesn't recognize that variables should
	// still be in scope there.
	filesemi := bytes.Join([][]byte{data[:cursor], []byte(";"), data[cursor:]}, nil)

	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, filename, filesemi, parser.AllErrors)
	if err != nil && c.debug {
		logParseError("Error parsing input file (outer block)", err)
	}
	pos := fset.File(fileAST.Pos()).Pos(cursor)

	var otherASTs []*ast.File
	for _, otherName := range c.findOtherPackageFiles(filename, fileAST.Name.Name) {
		ast, err := parser.ParseFile(fset, otherName, nil, 0)
		if err != nil && c.debug {
			logParseError("Error parsing other file", err)
		}
		otherASTs = append(otherASTs, ast)
	}

	var cfg types.Config
	cfg.Importer = importer.Default()
	cfg.Error = func(err error) {}
	pkg, _ := cfg.Check("", fset, append(otherASTs, fileAST), nil)

	return fset, pos, pkg
}

func (c *Suggester) fieldNameCandidates(typ types.Type, b *candidateCollector) {
	s := typ.Underlying().(*types.Struct)
	for i, n := 0, s.NumFields(); i < n; i++ {
		b.appendObject(s.Field(i))
	}
}

func (c *Suggester) typeSelectCandidates(typ0 types.Type, b *candidateCollector) {
	typ := typ0
	ptr, isPtr := typ.(*types.Pointer)
	if isPtr {
		typ = ptr.Elem()
	}
	named, isNamed := typ.(*types.Named)
	if !isNamed {
		return
	}

	// TODO(mdempsky): Should include inherited methods too.
	for i, n := 0, named.NumMethods(); i < n; i++ {
		f := named.Method(i)
		// Method set for *T includes T's.
		if isPtr || typ0 == f.Type().(*types.Signature).Recv().Type() {
			b.appendObject(f)
		}
	}
}

func (c *Suggester) valueSelectCandidates(typ0 types.Type, addressable bool, b *candidateCollector) {
	seenTyp := make(map[types.Type]bool)
	seenName := make(map[string]bool)

	// TODO(mdempsky): This is imprecise. It suggests ambiguous selections.
	addObj := func(obj types.Object) {
		name := obj.Name()
		if !seenName[name] {
			seenName[name] = true
			b.appendObject(obj)
		}
	}

	typs := []types.Type{typ0}
	for len(typs) > 0 {
		typ0 := typs[0]
		typs = typs[1:]

		// Prevent infinite loop due to:
		//   type S1 struct { *S2 }
		//   type S2 struct { *S1 }
		if seenTyp[typ0] {
			continue
		}
		seenTyp[typ0] = true

		// Look for named methods.
		typ := typ0
		ptr, isPtr := typ.(*types.Pointer)
		if isPtr {
			typ = ptr.Elem()
		}
		if named, isNamed := typ.(*types.Named); isNamed {
			for i, n := 0, named.NumMethods(); i < n; i++ {
				m := named.Method(i)

				_, isPtrMethod := m.Type().(*types.Signature).Recv().Type().(*types.Pointer)
				if !isPtrMethod || (isPtr || addressable) {
					addObj(m)
				}
			}
		}

		// Look for struct fields and interface methods.
		typ = typ0.Underlying()
		ptr, isPtr = typ.(*types.Pointer)
		if isPtr {
			typ = ptr.Elem().Underlying()
		}
		switch typ := typ.(type) {
		case *types.Interface:
			if !isPtr {
				// TODO(mdempsky): Dedup with logic above for Named.
				for i, n := 0, typ.NumMethods(); i < n; i++ {
					m := typ.Method(i)
					_, isPtrMethod := m.Type().(*types.Signature).Recv().Type().(*types.Pointer)
					if !isPtrMethod || (isPtr || addressable) {
						addObj(m)
					}
				}
			}
		case *types.Struct:
			for i, n := 0, typ.NumFields(); i < n; i++ {
				f := typ.Field(i)
				addObj(f)
				if f.Anonymous() {
					typs = append(typs, f.Type())
				}
			}
		}
	}
}

func (c *Suggester) packageCandidates(pkg *types.Package, b *candidateCollector) {
	c.scopeCandidates(pkg.Scope(), token.NoPos, b)
}

func (c *Suggester) scopeCandidates(scope *types.Scope, pos token.Pos, b *candidateCollector) {
	seen := make(map[string]bool)
	for scope != nil {
		isPkgScope := scope.Parent() == types.Universe
		for _, name := range scope.Names() {
			if seen[name] {
				continue
			}
			obj := scope.Lookup(name)
			if !isPkgScope && obj.Pos() > pos {
				continue
			}
			seen[name] = true
			b.appendObject(obj)
		}
		scope = scope.Parent()
	}
}

func logParseError(intro string, err error) {
	if el, ok := err.(scanner.ErrorList); ok {
		log.Printf("%s:", intro)
		for _, er := range el {
			log.Printf(" %s", er)
		}
	} else {
		log.Printf("%s: %s", intro, err)
	}
}

func (c *Suggester) findOtherPackageFiles(filename, pkgName string) []string {
	if filename == "" {
		return nil
	}

	dir, file := filepath.Split(filename)
	dents, err := ioutil.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	isTestFile := strings.HasSuffix(file, "_test.go")

	// TODO(mdempsky): Use go/build.(*Context).MatchFile or
	// something to properly handle build tags?
	var out []string
	for _, dent := range dents {
		name := dent.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if name == file || !strings.HasSuffix(name, ".go") {
			continue
		}
		if !isTestFile && strings.HasSuffix(name, "_test.go") {
			continue
		}

		abspath := filepath.Join(dir, name)
		if pkgNameFor(abspath) == pkgName {
			out = append(out, abspath)
		}
	}

	return out
}

func pkgNameFor(filename string) string {
	file, _ := parser.ParseFile(token.NewFileSet(), filename, nil, parser.PackageClauseOnly)
	return file.Name.Name
}
