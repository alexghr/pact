package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	positions := token.NewFileSet()
	failed := false
	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if path == "internal/docker" || entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(positions, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			usesExec := false
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if importPath == "os/exec" {
					usesExec = true
					break
				}
			}
			if !usesExec {
				return nil
			}
			for node := range ast.Preorder(file) {
				call, ok := node.(*ast.CallExpr)
				if ok && dockerCommand(call) {
					fmt.Fprintf(os.Stderr, "%s: Docker commands must use internal/docker\n", positions.Position(call.Pos()))
					failed = true
				}
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func dockerCommand(call *ast.CallExpr) bool {
	function, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	var argument int
	switch function.Sel.Name {
	case "Command":
		argument = 0
	case "CommandContext":
		argument = 1 // first arg is ctx
	default:
		return false
	}

	if len(call.Args) <= argument {
		return false
	}
	literal, ok := call.Args[argument].(*ast.BasicLit)
	if !ok {
		return false
	}
	program, err := strconv.Unquote(literal.Value)
	return err == nil && filepath.Base(program) == "docker"
}
