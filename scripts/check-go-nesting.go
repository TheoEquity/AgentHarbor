package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

type finding struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Depth int    `json:"depth"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run ./scripts/check-go-nesting.go <max-depth> <file>...")
		os.Exit(2)
	}
	maxDepth, err := strconv.Atoi(os.Args[1])
	if err != nil || maxDepth < 1 {
		fmt.Fprintln(os.Stderr, "max-depth must be a positive integer")
		os.Exit(2)
	}

	findings := []finding{}
	for _, path := range os.Args[2:] {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		ast.PreorderStack(file, nil, func(node ast.Node, ancestors []ast.Node) bool {
			if len(ancestors) == 0 || !countsTowardDepth(node, ancestors[len(ancestors)-1]) {
				return true
			}
			depth := 1
			for i := len(ancestors) - 1; i > 0; i-- {
				switch ancestors[i].(type) {
				case *ast.FuncDecl, *ast.FuncLit:
					if depth > maxDepth {
						findings = append(findings, finding{path, fset.Position(node.Pos()).Line, depth})
					}
					return true
				}
				if countsTowardDepth(ancestors[i], ancestors[i-1]) {
					depth++
				}
			}
			return true
		})
	}

	if err := json.NewEncoder(os.Stdout).Encode(findings); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func countsTowardDepth(node, parent ast.Node) bool {
	switch stmt := node.(type) {
	case *ast.IfStmt:
		// An else-if continues the same branch level.
		outerIf, isIf := parent.(*ast.IfStmt)
		return !isIf || outerIf.Else != stmt
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return true
	default:
		return false
	}
}
