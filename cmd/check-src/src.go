package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"sync"
)

type srcFiles struct {
	mu        sync.Mutex
	positions *token.FileSet
	files     map[string]*ast.File
}

func newSrcFiles() *srcFiles {
	return &srcFiles{
		positions: token.NewFileSet(),
		files:     make(map[string]*ast.File),
	}
}

func (s *srcFiles) ParseFile(path string) (*ast.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, ok := s.files[path]
	if ok {
		return file, nil
	}

	file, err := parser.ParseFile(s.positions, path, nil, parser.SkipObjectResolution)
	if err == nil {
		s.files[path] = file
	}

	return file, err
}

func (s *srcFiles) Position(p token.Pos) token.Position {
	return s.positions.Position(p)
}

type srcCheck interface {
	Check(*srcFiles, io.Writer) (bool, error)
}
