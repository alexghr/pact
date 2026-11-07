package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	positions := token.NewFileSet()
	camelCase := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	failed := false
	err := filepath.WalkDir("internal/config", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(positions, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for node := range ast.Preorder(file) {
			field, ok := node.(*ast.Field)
			if !ok || field.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				return fmt.Errorf("%s: %w", positions.Position(field.Tag.Pos()), err)
			}
			jsonTag, ok := reflect.StructTag(tag).Lookup("json")
			if !ok || jsonTag == "-" {
				continue
			}
			name, _, _ := strings.Cut(jsonTag, ",")
			if !camelCase.MatchString(name) {
				fmt.Fprintf(os.Stderr, "%s: config JSON name must be camelCase: %q\n", positions.Position(field.Tag.Pos()), name)
				failed = true
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}
