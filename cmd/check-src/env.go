package main

import (
	"fmt"
	"go/ast"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

type getenvOutsideConfig struct{}

func (getenvOutsideConfig) Check(src *srcFiles, out io.Writer) (bool, error) {
	failed := false

	for _, root := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if entry.IsDir() {
				if path == "internal/config" {
					return filepath.SkipDir
				}
				return nil
			}

			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, err := src.ParseFile(path)
			if err != nil {
				return err
			}

			// identify all modules which import os/exec
			usesOs := false
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if importPath == "os" {
					usesOs = true
					break
				}
			}

			if !usesOs {
				return nil
			}

			for node := range ast.Preorder(file) {
				call, ok := node.(*ast.CallExpr)
				if ok && callsGetenv(call) {
					fmt.Fprintf(out, "%s: Getenv must not be called outside internal/config\n", src.Position(call.Pos()))
					failed = true
				}
			}

			return nil
		})

		if err != nil {
			return failed, err
		}
	}

	return failed, nil
}

func callsGetenv(call *ast.CallExpr) bool {
	function, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	return function.Sel.Name == "Getenv"
}
