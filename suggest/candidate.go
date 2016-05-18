package suggest

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

type Candidate struct {
	Class string
	Name  string
	Type  string
}

func (c Candidate) Suggestion() string {
	switch {
	case c.Class != "func":
		return c.Name
	case strings.HasPrefix(c.Type, "func()"):
		return c.Name + "()"
	default:
		return c.Name + "("
	}
}

func (c Candidate) String() string {
	if c.Class == "func" {
		return fmt.Sprintf("%s %s%s", c.Class, c.Name, strings.TrimPrefix(c.Type, "func"))
	}
	return fmt.Sprintf("%s %s %s", c.Class, c.Name, c.Type)
}

type candidatesByClassAndName []Candidate

func (s candidatesByClassAndName) Len() int      { return len(s) }
func (s candidatesByClassAndName) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s candidatesByClassAndName) Less(i, j int) bool {
	if s[i].Class != s[j].Class {
		return s[i].Class < s[j].Class
	}
	return s[i].Name < s[j].Name
}

type objectFilter func(types.Object) bool

var objectFilters = map[string]objectFilter{
	"const":   func(obj types.Object) bool { _, ok := obj.(*types.Const); return ok },
	"func":    func(obj types.Object) bool { _, ok := obj.(*types.Func); return ok },
	"package": func(obj types.Object) bool { _, ok := obj.(*types.PkgName); return ok },
	"type":    func(obj types.Object) bool { _, ok := obj.(*types.TypeName); return ok },
	"var":     func(obj types.Object) bool { _, ok := obj.(*types.Var); return ok },
}

func classifyObject(obj types.Object) string {
	switch obj.(type) {
	case *types.Builtin:
		return "func"
	case *types.Const:
		return "const"
	case *types.Func:
		return "func"
	case *types.PkgName:
		return "package"
	case *types.TypeName:
		return "type"
	case *types.Var:
		return "var"
	}
	panic(fmt.Sprintf("unhandled types.Object: %T", obj))
}

type candidateCollector struct {
	candidates []Candidate
	exact      []types.Object
	badcase    []types.Object
	localpkg   *types.Package
	partial    string
	filter     objectFilter
}

func (b *candidateCollector) getCandidates() []Candidate {
	objs := b.exact
	if objs == nil {
		objs = b.badcase
	}

	res := b.candidates
	for _, obj := range objs {
		res = append(res, b.asCandidate(obj))
	}
	sort.Sort(candidatesByClassAndName(res))
	return res
}

func (b *candidateCollector) asCandidate(obj types.Object) Candidate {
	objClass := classifyObject(obj)
	var typ types.Type
	switch objClass {
	case "const", "func", "var":
		typ = obj.Type()
	case "type":
		typ = obj.Type().Underlying()
	}

	var typStr string
	switch t := typ.(type) {
	case *types.Interface:
		typStr = "interface"
	case *types.Struct:
		typStr = "struct"
	default:
		if _, isBuiltin := obj.(*types.Builtin); isBuiltin {
			if obj.Pkg().Path() == "unsafe" {
				typStr = "func(any) uintptr"
			} else {
				panic("TODO: unhandled builtin")
			}
		} else if t != nil {
			typStr = types.TypeString(t, b.qualify)
		}
	}

	return Candidate{
		Class: objClass,
		Name:  obj.Name(),
		Type:  typStr,
	}
}

func (b *candidateCollector) qualify(pkg *types.Package) string {
	if pkg == b.localpkg {
		return ""
	}
	return pkg.Name()
}

func (b *candidateCollector) appendImport(path string) {
	b.candidates = append(b.candidates, Candidate{Class: "import", Name: path})
}

func (b *candidateCollector) appendObject(obj types.Object) {
	// TODO(mdempsky): Change this to true.
	const proposeBuiltins = false

	if !proposeBuiltins && obj.Pkg() == nil && obj.Name() != "Error" {
		return
	}

	if obj.Pkg() != nil && obj.Pkg() != b.localpkg && !obj.Exported() {
		return
	}

	// TODO(mdempsky): Reconsider this functionality.
	if b.filter != nil && !b.filter(obj) {
		return
	}

	if b.filter != nil || strings.HasPrefix(obj.Name(), b.partial) {
		b.exact = append(b.exact, obj)
	} else if strings.HasPrefix(strings.ToLower(obj.Name()), strings.ToLower(b.partial)) {
		b.badcase = append(b.badcase, obj)
	}
}
