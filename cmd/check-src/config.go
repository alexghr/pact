package main

import (
	"fmt"
	"go/ast"
	"io"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// ensure all config fields in internal/config use camelCase
type configCamelCase struct{}

func (c configCamelCase) Check(src *srcFiles, out io.Writer) (bool, error) {
	camelCase := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	failed := false

	err := filepath.WalkDir("internal/config", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := src.ParseFile(path)
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
				return fmt.Errorf("%s: %w", src.Position(field.Tag.Pos()), err)
			}
			jsonTag, ok := reflect.StructTag(tag).Lookup("json")
			if !ok || jsonTag == "-" {
				continue
			}
			name, _, _ := strings.Cut(jsonTag, ",")
			if !camelCase.MatchString(name) {
				fmt.Fprintf(out, "%s: config JSON name must be camelCase: %q\n", src.Position(field.Tag.Pos()), name)
				failed = true
			}
		}
		return nil
	})

	return failed, err
}
