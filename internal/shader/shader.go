// Copyright 2020 The Ebiten Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package shader

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/hajimehoshi/ebiten/internal/shaderir"
)

type variable struct {
	name string
	typ  typ
	init ast.Expr
}

type constant struct {
	name string
	typ  typ
	init ast.Expr
}

type function struct {
	name  string
	block *block

	ir shaderir.Func
}

type compileState struct {
	fs *token.FileSet

	ir shaderir.Program

	// uniforms is a collection of uniform variable names.
	uniforms []string

	global block

	errs []string
}

type block struct {
	types  []typ
	vars   []variable
	consts []constant
	funcs  []function
	pos    token.Pos
	outer  *block

	ir shaderir.Block
}

type ParseError struct {
	errs []string
}

func (p *ParseError) Error() string {
	return strings.Join(p.errs, "\n")
}

func Compile(src []byte) (*shaderir.Program, error) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "", src, parser.AllErrors)
	if err != nil {
		return nil, err
	}

	s := &compileState{
		fs: fs,
	}
	s.parse(f)

	if len(s.errs) > 0 {
		return nil, &ParseError{s.errs}
	}

	// TODO: Resolve identifiers?
	// TODO: Resolve constants

	// TODO: Make a call graph and reorder the elements.
	return &s.ir, nil
}

func (s *compileState) addError(pos token.Pos, str string) {
	p := s.fs.Position(pos)
	s.errs = append(s.errs, fmt.Sprintf("%s: %s", p, str))
}

func (cs *compileState) parse(f *ast.File) {
	for _, d := range f.Decls {
		cs.parseDecl(&cs.global, d, true)
	}

	if len(cs.errs) > 0 {
		return
	}

	for _, f := range cs.global.funcs {
		cs.ir.Funcs = append(cs.ir.Funcs, f.ir)
	}
}

func (cs *compileState) parseDecl(b *block, d ast.Decl, global bool) {
	switch d := d.(type) {
	case *ast.GenDecl:
		switch d.Tok {
		case token.TYPE:
			// TODO: Parse other types
			for _, s := range d.Specs {
				s := s.(*ast.TypeSpec)
				t := cs.parseType(s.Type)
				t.name = s.Name.Name
				b.types = append(b.types, t)
			}
		case token.CONST:
			for _, s := range d.Specs {
				s := s.(*ast.ValueSpec)
				cs := cs.parseConstant(s)
				b.consts = append(b.consts, cs...)
			}
		case token.VAR:
			for _, s := range d.Specs {
				s := s.(*ast.ValueSpec)
				vs := cs.parseVariable(b, s)
				if !global {
					b.vars = append(b.vars, vs...)
					continue
				}
				for i, v := range vs {
					if v.name[0] < 'A' || 'Z' < v.name[0] {
						cs.addError(s.Names[i].Pos(), fmt.Sprintf("global variables must be exposed: %s", v.name))
					}
					// TODO: Check RHS
					cs.uniforms = append(cs.uniforms, v.name)
					cs.ir.Uniforms = append(cs.ir.Uniforms, v.typ.ir)
				}
			}
		case token.IMPORT:
			cs.addError(d.Pos(), "import is forbidden")
		default:
			cs.addError(d.Pos(), "unexpected token")
		}
	case *ast.FuncDecl:
		b.funcs = append(b.funcs, cs.parseFunc(d, b))
	default:
		cs.addError(d.Pos(), "unexpected decl")
	}
}

func (s *compileState) parseVariable(block *block, vs *ast.ValueSpec) []variable {
	var t typ
	if vs.Type != nil {
		t = s.parseType(vs.Type)
	}

	var vars []variable
	for i, n := range vs.Names {
		var init ast.Expr
		if len(vs.Values) > 0 {
			init = vs.Values[i]
			if t.ir.Main == shaderir.None {
				t = s.detectType(block, init)
			}
		}
		name := n.Name
		vars = append(vars, variable{
			name: name,
			typ:  t,
			init: init,
		})
	}
	return vars
}

func (s *compileState) parseConstant(vs *ast.ValueSpec) []constant {
	var t typ
	if vs.Type != nil {
		t = s.parseType(vs.Type)
	}

	var cs []constant
	for i, n := range vs.Names {
		cs = append(cs, constant{
			name: n.Name,
			typ:  t,
			init: vs.Values[i],
		})
	}
	return cs
}

func (cs *compileState) parseFunc(d *ast.FuncDecl, block *block) function {
	if d.Name == nil {
		cs.addError(d.Pos(), "function must have a name")
		return function{}
	}
	if d.Body == nil {
		cs.addError(d.Pos(), "function must have a body")
		return function{}
	}

	var inT []shaderir.Type
	var inParams []variable
	for _, f := range d.Type.Params.List {
		t := cs.parseType(f.Type)
		for _, n := range f.Names {
			inParams = append(inParams, variable{
				name: n.Name,
				typ:  t,
			})
			inT = append(inT, t.ir)
		}
	}

	var outT []shaderir.Type
	var outParams []variable
	if d.Type.Results != nil {
		for _, f := range d.Type.Results.List {
			t := cs.parseType(f.Type)
			if len(f.Names) == 0 {
				outParams = append(outParams, variable{
					name: "",
					typ:  t,
				})
				outT = append(outT, t.ir)
			} else {
				for _, n := range f.Names {
					outParams = append(outParams, variable{
						name: n.Name,
						typ:  t,
					})
					outT = append(outT, t.ir)
				}
			}
		}
	}

	b := cs.parseBlock(block, d.Body, inParams, outParams)

	return function{
		name:  d.Name.Name,
		block: b,
		ir: shaderir.Func{
			Index:     len(cs.ir.Funcs),
			InParams:  inT,
			OutParams: outT,
			Block:     b.ir,
		},
	}
}

func (cs *compileState) parseBlock(outer *block, b *ast.BlockStmt, inParams, outParams []variable) *block {
	vars := make([]variable, 0, len(inParams)+len(outParams))
	vars = append(vars, inParams...)
	vars = append(vars, outParams...)
	block := &block{
		vars:  vars,
		outer: outer,
	}

	for _, l := range b.List {
		switch l := l.(type) {
		case *ast.AssignStmt:
			switch l.Tok {
			case token.DEFINE:
				for i, s := range l.Lhs {
					v := variable{
						name: s.(*ast.Ident).Name,
					}
					if len(l.Rhs) > 0 {
						v.typ = cs.detectType(block, l.Rhs[i])
					}
					block.vars = append(block.vars, v)
				}
				for range l.Rhs {
					/*block.stmts = append(block.stmts, stmt{
						stmtType: stmtAssign,
						exprs:    []ast.Expr{l.Lhs[i], l.Rhs[i]},
					})*/
				}
			case token.ASSIGN:
				// TODO: What about the statement `a,b = b,a?`
				for range l.Rhs {
					/*block.stmts = append(block.stmts, stmt{
						stmtType: stmtAssign,
						exprs:    []ast.Expr{l.Lhs[i], l.Rhs[i]},
					})*/
				}
			}
		case *ast.BlockStmt:
			/*block.stmts = append(block.stmts, stmt{
				stmtType: stmtBlock,
				block:    cs.parseBlock(block, l),
			})*/
		case *ast.DeclStmt:
			cs.parseDecl(block, l.Decl, false)
		case *ast.ReturnStmt:
			for i, r := range l.Results {
				e := cs.parseExpr(block, r)
				block.ir.Stmts = append(block.ir.Stmts, shaderir.Stmt{
					Type: shaderir.Assign,
					Exprs: []shaderir.Expr{
						{
							Type:  shaderir.LocalVariable,
							Index: len(inParams) + i,
						},
						e,
					},
				})
			}
			block.ir.Stmts = append(block.ir.Stmts, shaderir.Stmt{
				Type: shaderir.Return,
			})
		}
	}

	return block
}

func (s *compileState) detectType(b *block, expr ast.Expr) typ {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.FLOAT {
			return typ{
				ir: shaderir.Type{Main: shaderir.Float},
			}
		}
		if e.Kind == token.INT {
			return typ{
				ir: shaderir.Type{Main: shaderir.Int},
			}
		}
		s.addError(expr.Pos(), fmt.Sprintf("unexpected literal: %s", e.Value))
		return typ{}
	case *ast.CompositeLit:
		return s.parseType(e.Type)
	case *ast.Ident:
		n := e.Name
		for _, v := range b.vars {
			if v.name == n {
				return v.typ
			}
		}
		if b == &s.global {
			for i, v := range s.uniforms {
				if v == n {
					return typ{ir: s.ir.Uniforms[i]}
				}
			}
		}
		if b.outer != nil {
			return s.detectType(b.outer, e)
		}
		s.addError(expr.Pos(), fmt.Sprintf("unexpected identity: %s", n))
		return typ{}
	//case *ast.SelectorExpr:
	//return fmt.Sprintf("%s.%s", dumpExpr(e.X), dumpExpr(e.Sel))
	default:
		s.addError(expr.Pos(), fmt.Sprintf("detecting type not implemented: %#v", expr))
		return typ{}
	}
}

func (b *block) findLocalVariable(name string) (int, bool) {
	for i, v := range b.vars {
		if v.name == name {
			return i, true
		}
	}
	if b.outer != nil {
		return b.outer.findLocalVariable(name)
	}
	return 0, false
}

func (cs *compileState) parseExpr(block *block, expr ast.Expr) shaderir.Expr {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			v, err := strconv.ParseInt(e.Value, 10, 32)
			if err != nil {
				cs.addError(e.Pos(), fmt.Sprintf("unexpected literal: %s", e.Value))
				return shaderir.Expr{}
			}
			return shaderir.Expr{
				Type: shaderir.IntExpr,
				Int:  int32(v),
			}
		case token.FLOAT:
			v, err := strconv.ParseFloat(e.Value, 32)
			if err != nil {
				cs.addError(e.Pos(), fmt.Sprintf("unexpected literal: %s", e.Value))
				return shaderir.Expr{}
			}
			return shaderir.Expr{
				Type:  shaderir.FloatExpr,
				Float: float32(v),
			}
		default:
			cs.addError(e.Pos(), fmt.Sprintf("literal not implemented: %#v", e))
		}
	case *ast.CallExpr:
		exprs := []shaderir.Expr{
			cs.parseExpr(block, e.Fun),
		}
		for _, a := range e.Args {
			exprs = append(exprs, cs.parseExpr(block, a))
		}
		return shaderir.Expr{
			Type:  shaderir.Call,
			Exprs: exprs,
		}
	case *ast.Ident:
		i, ok := block.findLocalVariable(e.Name)
		if ok {
			return shaderir.Expr{
				Type:  shaderir.LocalVariable,
				Index: i,
			}
		}
		f, ok := shaderir.ParseBuiltinFunc(e.Name)
		if ok {
			return shaderir.Expr{
				Type:        shaderir.BuiltinFuncExpr,
				BuiltinFunc: f,
			}
		}
		cs.addError(e.Pos(), fmt.Sprintf("unexpected identifier: %s", e.Name))
	case *ast.SelectorExpr:
		return shaderir.Expr{
			Type: shaderir.FieldSelector,
			Exprs: []shaderir.Expr{
				cs.parseExpr(block, e.X),
				{
					Type:      shaderir.SwizzlingExpr,
					Swizzling: e.Sel.Name,
				},
			},
		}
	default:
		cs.addError(e.Pos(), fmt.Sprintf("expression not implemented: %#v", e))
	}
	return shaderir.Expr{}
}
