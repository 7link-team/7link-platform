package i18n

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type activitySource struct {
	path      string
	file      *ast.File
	constants map[string]ast.Expr
	imports   map[string]string
}
type activityValidator struct {
	root     string
	sources  map[string]*activitySource
	packages map[string]map[string]ast.Expr
	counts   map[string]int
}

func (validator *activityValidator) evaluate(s *activitySource, x ast.Expr, local map[string]string) (string, error) {
	switch v := x.(type) {
	case *ast.ParenExpr:
		return validator.evaluate(s, v.X, local)
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			return strconv.Unquote(v.Value)
		}
	case *ast.Ident:
		if text, ok := local[v.Name]; ok {
			return text, nil
		}
		if v.Obj != nil && v.Obj.Kind == ast.Con {
			if spec, ok := v.Obj.Decl.(*ast.ValueSpec); ok {
				for i, id := range spec.Names {
					if id.Name == v.Name && i < len(spec.Values) {
						return validator.evaluate(s, spec.Values[i], local)
					}
				}
			}
		}
		if expression, ok := s.constants[v.Name]; ok {
			return validator.evaluate(s, expression, local)
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			a, e := validator.evaluate(s, v.X, local)
			if e != nil {
				return "", e
			}
			b, e := validator.evaluate(s, v.Y, local)
			return a + b, e
		}
	case *ast.SelectorExpr:
		if id, ok := v.X.(*ast.Ident); ok {
			path := s.imports[id.Name]
			target := validator.sources[filepath.Join(validator.root, strings.TrimPrefix(path, "7link.com/platform/"), "i18n.go")]
			if target != nil {
				return validator.evaluate(target, &ast.Ident{Name: v.Sel.Name}, nil)
			}
		}
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "ReplaceAll" && len(v.Args) == 3 {
			a, e := validator.evaluate(s, v.Args[0], local)
			if e != nil {
				return "", e
			}
			b, e := validator.evaluate(s, v.Args[1], local)
			if e != nil {
				return "", e
			}
			c, e := validator.evaluate(s, v.Args[2], local)
			if e != nil {
				return "", e
			}
			return strings.ReplaceAll(a, b, c), nil
		}
	}
	return "", fmt.Errorf("unresolved %T in %s", x, s.path)
}

type activityVisitor struct {
	a      *activityValidator
	s      *activitySource
	locals []map[string]string
	parent ast.Node
}

func (v activityVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}
	next := v
	next.parent = n
	if loop, ok := n.(*ast.RangeStmt); ok {
		id, ok := loop.Value.(*ast.Ident)
		if !ok {
			return next
		}
		list, ok := loop.X.(*ast.CompositeLit)
		if !ok {
			return next
		}
		var expanded []map[string]string
		for _, local := range v.locals {
			for _, entry := range list.Elts {
				value, e := v.a.evaluate(v.s, entry, local)
				if e != nil {
					continue
				}
				clone := map[string]string{}
				for k, x := range local {
					clone[k] = x
				}
				clone[id.Name] = value
				expanded = append(expanded, clone)
			}
		}
		if len(expanded) > 0 {
			next.locals = expanded
		}
	}
	literal, ok := n.(*ast.CompositeLit)
	if !ok {
		return next
	}
	fields := map[string]ast.Expr{}
	for _, item := range literal.Elts {
		if kv, ok := item.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok {
				fields[key.Name] = kv.Value
			}
		}
	}
	if fields["DisplayNameKey"] == nil {
		declaredHandler := fields["Kind"] != nil && fields["Run"] != nil
		declaredSubscriber := fields["EventTypes"] != nil && fields["Handle"] != nil
		declaredEvent := fields["Name"] != nil && fields["SchemaVersion"] != nil && fields["Realtime"] != nil
		// Ephemeral publication constructs a runtime spec from e.Type; it is
		// deliberately not a registered durable event declaration.
		if _, runtimeType := fields["Name"].(*ast.SelectorExpr); runtimeType {
			declaredEvent = false
		}
		if declaredHandler || declaredSubscriber || declaredEvent {
			panic(fmt.Sprintf("missing activity presentation in %s", v.s.path))
		}
		return next
	}
	codeField, category := "Name", "event"
	if fields["Kind"] != nil && fields["Run"] != nil {
		codeField, category = "Kind", "job"
	} else if fields["Handle"] != nil {
		category = "subscriber"
	}
	for _, local := range v.locals {
		code, e := v.a.evaluate(v.s, fields[codeField], local)
		activityMust(e)
		for _, field := range []string{"DisplayNameKey", "DescriptionKey"} {
			key, e := v.a.evaluate(v.s, fields[field], local)
			activityMust(e)
			namespace, localKey, ok := strings.Cut(key, ".")
			if !ok {
				panic(key)
			}
			suffix := "_name"
			if field == "DescriptionKey" {
				suffix = "_description"
			}
			expected := "activity_" + category + "_" + strings.ReplaceAll(code, ".", "_") + suffix
			if localKey != expected {
				panic(fmt.Sprintf("identity mismatch %s %s: %s != %s", v.s.path, code, localKey, expected))
			}
			bundleDir := filepath.Join(filepath.Dir(v.s.path), "i18n", "locales")
			for _, locale := range []string{"en-US", "zh-CN", "ms-MY"} {
				data, e := os.ReadFile(filepath.Join(bundleDir, locale+".json"))
				activityMust(e)
				var messages map[string]string
				activityMust(json.Unmarshal(data, &messages))
				if messages[localKey] == "" {
					panic(fmt.Sprintf("missing %s %s %s", namespace, localKey, locale))
				}
			}
		}
		v.a.counts[category]++
	}
	return next
}
func activityMust(e error) {
	if e != nil {
		panic(e)
	}
}

// Parse real production declarations, expanding constant string loops. This checks
// the complete set, including direct registrations and prefixed loop names.
func TestProductionActivityBindingsMatchTheirIdentityAndAllLocales(t *testing.T) {
	defer func() {
		if failure := recover(); failure != nil {
			t.Fatal(failure)
		}
	}()
	a := &activityValidator{root: filepath.Join("..", ".."), sources: map[string]*activitySource{}, packages: map[string]map[string]ast.Expr{}, counts: map[string]int{}}

	activityMust(filepath.WalkDir(filepath.Join(a.root, "internal"), func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(path, ".gen.") {
			return nil
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if e != nil {
			return e
		}
		dir := filepath.Dir(path)
		if a.packages[dir] == nil {
			a.packages[dir] = map[string]ast.Expr{}
		}
		s := &activitySource{path: path, file: f, constants: a.packages[dir], imports: map[string]string{}}
		for _, imp := range f.Imports {
			value, e := strconv.Unquote(imp.Path.Value)
			if e != nil {
				return e
			}
			name := filepath.Base(value)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			s.imports[name] = value
		}
		for _, declaration := range f.Decls {
			if group, ok := declaration.(*ast.GenDecl); ok && group.Tok == token.CONST {
				for _, spec := range group.Specs {
					if value, ok := spec.(*ast.ValueSpec); ok {
						for i, name := range value.Names {
							if i < len(value.Values) {
								s.constants[name.Name] = value.Values[i]
							}
						}
					}
				}
			}
		}
		a.sources[path] = s
		return nil
	}))
	for _, s := range a.sources {
		ast.Walk(activityVisitor{a: a, s: s, locals: []map[string]string{{}}}, s.file)
	}
	for category, minimum := range map[string]int{"event": 300, "job": 50, "subscriber": 20} {
		if a.counts[category] < minimum {
			t.Fatalf("production %s scan too small: %d", category, a.counts[category])
		}
	}
	t.Logf("verified %v with names and purposes in all three locales", a.counts)
}
