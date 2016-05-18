package lookdot

import "go/types"

type Visitor func(obj types.Object)

func Walk(tv *types.TypeAndValue, v Visitor) bool {
	switch {
	case tv.IsType():
		// Anonymous types may have methods too (e.g.,
		// interfaces or structs with embedded fields), but
		// the Go spec restricts method expressions to named
		// types and pointers to named types.
		if namedOf(tv.Type) != nil {
			walk(tv.Type, false, false, v)
		}
	case tv.IsValue():
		walk(tv.Type, tv.Addressable(), true, v)
	default:
		return false
	}
	return true
}

func walk(typ0 types.Type, addable0, value bool, v Visitor) {
	// Enumerating valid selector expression identifiers is
	// surprisingly nuanced.

	// found is a map from selector identifiers to the objects
	// they select. Nil entries are used to track objects that
	// have already been reported to the visitor and to indicate
	// ambiguous identifiers.
	found := make(map[string]types.Object)

	addObj := func(id string, obj types.Object) {
		switch otherObj, isPresent := found[id]; {
		case !isPresent:
			found[id] = obj
		case otherObj != nil:
			// Ambiguous selector.
			found[id] = nil
		}
	}

	// visited keeps track of named types that we've already
	// visited. We only need to track named types, because
	// recursion can only happen through embedded struct fields,
	// which must be either a named type or a pointer to a named
	// type.
	visited := make(map[*types.Named]bool)

	type todo struct {
		typ     types.Type
		addable bool
	}

	var cur, next []todo
	cur = []todo{{typ0, addable0}}

	for {
		if len(cur) == 0 {
			// Flush discovered objects to visitor function.
			for id, obj := range found {
				if obj != nil {
					v(obj)
					found[id] = nil
				}
			}

			// Move unvisited types from next to cur.
			// It's important to check between levels to
			// ensure that ambiguous selections are
			// correctly handled.
			cur = next[:0]
			for _, t := range next {
				nt := namedOf(t.typ)
				if nt == nil {
					panic("embedded struct field without name?")
				}
				if !visited[nt] {
					cur = append(cur, t)
				}
			}
			next = nil

			if len(cur) == 0 {
				break
			}
		}

		now := cur[0]
		cur = cur[1:]

		// Look for methods declared on a named type.
		{
			typ := now.typ
			addable := now.addable
			ptr, isPtr := typ.(*types.Pointer)
			if isPtr {
				typ = ptr.Elem()
				addable = true
			}

			if named, isNamed := typ.(*types.Named); isNamed {
				visited[named] = true
				for i, n := 0, named.NumMethods(); i < n; i++ {
					m := types.Object(named.Method(i))
					id := m.Id()
					if !addable {
						_, isPtrMethod := m.Type().(*types.Signature).Recv().Type().(*types.Pointer)
						if isPtrMethod {
							addObj(id, nil)
							continue
						}
					}
					addObj(id, m)
				}
			}
		}

		// Look for struct fields and interface methods.
		{
			typ := now.typ.Underlying()
			addable := now.addable
			ptr, isPtr := typ.(*types.Pointer)
			if isPtr {
				typ = ptr.Elem().Underlying()
				addable = true
			}

			switch typ := typ.(type) {
			case *types.Interface:
				if isPtr {
					break
				}
				for i, n := 0, typ.NumMethods(); i < n; i++ {
					m := typ.Method(i)
					addObj(m.Id(), m)
				}
			case *types.Struct:
				for i, n := 0, typ.NumFields(); i < n; i++ {
					f := typ.Field(i)
					if f.Anonymous() {
						next = append(next, todo{f.Type(), addable})
					}
					id := f.Id()
					if value {
						addObj(id, f)
					} else {
						addObj(id, nil)
					}
				}
			}
		}
	}
}

// namedOf returns the named type T when given T or *T.
// Otherwise, it returns nil.
func namedOf(typ types.Type) *types.Named {
	if ptr, isPtr := typ.(*types.Pointer); isPtr {
		typ = ptr.Elem()
	}
	res, _ := typ.(*types.Named)
	return res
}
