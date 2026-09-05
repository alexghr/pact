package artifacts

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/alexghr/pact/internal/state"
)

const (
	defaultReadBytes = 64 << 10
	maxReadBytes     = 256 << 10
)

type CreateArtifactInput struct {
	Name        string `json:"name" jsonschema:"Human-readable name including recognizable project and topic identifiers."`
	Description string `json:"description,omitempty" jsonschema:"Searchable description of the projects, purpose, and contents. Do not include secrets."`
}

type SearchArtifactsInput struct {
	Query                string `json:"query,omitempty" jsonschema:"Optional case-insensitive substring to find in artifact names, descriptions, or file paths."`
	CreatorPactSessionID int64  `json:"creator_pact_session_id,omitempty" jsonschema:"Optional creating Pact session ID. Omit to search artifacts from every session."`
	Limit                int    `json:"limit,omitempty" jsonschema:"Maximum number of results from 1 to 100. Defaults to 50."`
	Offset               int    `json:"offset,omitempty" jsonschema:"Zero-based result offset for pagination. Defaults to 0."`
}

type GetArtifactInput struct {
	ArtifactID int64 `json:"artifact_id" jsonschema:"Artifact ID returned by create_artifact or search_artifacts."`
}

type UpdateArtifactInput struct {
	ArtifactID       int64   `json:"artifact_id" jsonschema:"Artifact ID to update."`
	Name             *string `json:"name,omitempty" jsonschema:"New artifact name. Omit to preserve the current name."`
	Description      *string `json:"description,omitempty" jsonschema:"New artifact description. Omit to preserve the current description."`
	ExpectedRevision int64   `json:"expected_revision" jsonschema:"Current artifact revision from get_artifact. A stale revision fails without changing the artifact."`
}

type ReadFileInput struct {
	ArtifactID int64  `json:"artifact_id" jsonschema:"Artifact containing the file."`
	Path       string `json:"path" jsonschema:"Normalized, relative, slash-separated file path within the artifact."`
	Offset     int64  `json:"offset,omitempty" jsonschema:"Zero-based byte offset. Defaults to 0."`
	Limit      int64  `json:"limit,omitempty" jsonschema:"Maximum bytes to return from 1 to 262144. Defaults to 65536."`
}

type ReadFileOutput struct {
	ArtifactID             int64  `json:"artifact_id"`
	Path                   string `json:"path"`
	MediaType              string `json:"media_type"`
	Encoding               string `json:"encoding"`
	Content                string `json:"content"`
	Offset                 int64  `json:"offset"`
	NextOffset             int64  `json:"next_offset"`
	SizeBytes              int64  `json:"size_bytes"`
	EOF                    bool   `json:"eof"`
	SHA256                 string `json:"sha256"`
	Version                int64  `json:"version"`
	UpdatedByPactSessionID int64  `json:"updated_by_pact_session_id"`
}

type WriteFileInput struct {
	ArtifactID      int64  `json:"artifact_id" jsonschema:"Artifact to receive the file."`
	Path            string `json:"path" jsonschema:"Normalized, relative, slash-separated path within the artifact."`
	Content         string `json:"content" jsonschema:"Complete replacement file content, encoded according to encoding."`
	Encoding        string `json:"encoding,omitempty" jsonschema:"Content encoding: utf-8 (default) or base64."`
	MediaType       string `json:"media_type,omitempty" jsonschema:"Optional MIME media type. When omitted it is inferred from the path and content."`
	ExpectedVersion int64  `json:"expected_version" jsonschema:"Use 0 to create a new file, or the current file version to replace it. A stale version fails without changing the file."`
}

type WriteFileOutput struct {
	File             state.ArtifactFile `json:"file"`
	ArtifactRevision int64              `json:"artifact_revision"`
}

type EditFileInput struct {
	ArtifactID      int64  `json:"artifact_id" jsonschema:"Artifact containing the UTF-8 text file."`
	Path            string `json:"path" jsonschema:"Normalized, relative, slash-separated file path within the artifact."`
	OldText         string `json:"old_text" jsonschema:"Exact non-empty text to replace."`
	NewText         string `json:"new_text" jsonschema:"Replacement text."`
	ReplaceAll      bool   `json:"replace_all,omitempty" jsonschema:"Replace every match. When false, old_text must occur exactly once."`
	ExpectedVersion int64  `json:"expected_version" jsonschema:"Current file version from read_artifact_file or get_artifact. The edit fails if the file has changed."`
}

type EditFileOutput struct {
	File             state.ArtifactFile `json:"file"`
	ArtifactRevision int64              `json:"artifact_revision"`
	Replacements     int                `json:"replacements"`
}

// Service binds artifact mutations to the session chosen by the host.
type Service struct {
	store         *state.Store
	pactSessionID int64
}

func NewService(store *state.Store, pactSessionID int64) *Service {
	return &Service{store: store, pactSessionID: pactSessionID}
}

func (s *Service) CreateArtifact(
	ctx context.Context,
	input CreateArtifactInput,
) (state.Artifact, error) {
	return s.store.CreateArtifact(ctx, s.pactSessionID, input.Name, input.Description)
}

func (s *Service) SearchArtifacts(ctx context.Context, input SearchArtifactsInput) (state.ArtifactPage, error) {
	return s.store.SearchArtifacts(ctx, state.ArtifactSearch{
		Query:                input.Query,
		CreatorPactSessionID: input.CreatorPactSessionID,
		Limit:                input.Limit,
		Offset:               input.Offset,
	})
}

func (s *Service) GetArtifact(
	ctx context.Context,
	input GetArtifactInput,
) (state.Artifact, error) {
	return s.store.GetArtifact(ctx, input.ArtifactID)
}

func (s *Service) UpdateArtifact(
	ctx context.Context,
	input UpdateArtifactInput,
) (state.Artifact, error) {
	return s.store.UpdateArtifact(ctx, state.ArtifactUpdate{
		ID:                  input.ArtifactID,
		EditorPactSessionID: s.pactSessionID,
		Name:                input.Name,
		Description:         input.Description,
		ExpectedRevision:    input.ExpectedRevision,
	})
}

func (s *Service) ReadFile(
	ctx context.Context,
	input ReadFileInput,
) (ReadFileOutput, error) {
	if input.Limit == 0 {
		input.Limit = defaultReadBytes
	}
	if input.Limit < 0 || input.Limit > maxReadBytes {
		return ReadFileOutput{}, fmt.Errorf("limit must be between 1 and %d", maxReadBytes)
	}
	chunk, err := s.store.ReadArtifactFile(ctx, input.ArtifactID, input.Path, input.Offset, input.Limit)
	if err != nil {
		return ReadFileOutput{}, err
	}
	encoding, content := encodeContent(chunk.MediaType, chunk.Content)
	nextOffset := chunk.Offset + int64(len(chunk.Content))
	return ReadFileOutput{
		ArtifactID:             chunk.ArtifactID,
		Path:                   chunk.Path,
		MediaType:              chunk.MediaType,
		Encoding:               encoding,
		Content:                content,
		Offset:                 chunk.Offset,
		NextOffset:             nextOffset,
		SizeBytes:              chunk.SizeBytes,
		EOF:                    nextOffset >= chunk.SizeBytes,
		SHA256:                 chunk.SHA256,
		Version:                chunk.Version,
		UpdatedByPactSessionID: chunk.UpdatedByPactSessionID,
	}, nil
}

func (s *Service) WriteFile(
	ctx context.Context,
	input WriteFileInput,
) (WriteFileOutput, error) {
	content, err := decodeContent(input.Encoding, input.Content)
	if err != nil {
		return WriteFileOutput{}, err
	}
	mediaType, err := normalizeMediaType(input.MediaType, input.Path, content)
	if err != nil {
		return WriteFileOutput{}, err
	}
	file, revision, err := s.store.WriteArtifactFile(ctx, state.ArtifactFileWrite{
		ArtifactID:          input.ArtifactID,
		EditorPactSessionID: s.pactSessionID,
		Path:                input.Path,
		MediaType:           mediaType,
		Content:             content,
		ExpectedVersion:     input.ExpectedVersion,
	})
	return WriteFileOutput{File: file, ArtifactRevision: revision}, err
}

func (s *Service) EditFile(
	ctx context.Context,
	input EditFileInput,
) (EditFileOutput, error) {
	if input.OldText == "" {
		return EditFileOutput{}, errors.New("old_text must not be empty")
	}
	file, err := s.store.GetArtifactFile(ctx, input.ArtifactID, input.Path)
	if err != nil {
		return EditFileOutput{}, err
	}
	if !utf8.Valid(file.Content) {
		return EditFileOutput{}, errors.New("artifact file is not valid UTF-8; use write_artifact_file with base64 content")
	}
	if input.ExpectedVersion != file.Version {
		return EditFileOutput{}, fmt.Errorf("edit artifact file: %w: current version is %d", state.ErrArtifactConflict, file.Version)
	}
	occurrences := strings.Count(string(file.Content), input.OldText)
	if occurrences == 0 {
		return EditFileOutput{}, errors.New("old_text was not found in the artifact file")
	}
	if !input.ReplaceAll && occurrences != 1 {
		return EditFileOutput{}, fmt.Errorf("old_text occurs %d times; provide more context or set replace_all", occurrences)
	}
	replacements := 1
	if input.ReplaceAll {
		replacements = occurrences
	}
	// Bound expansion before strings.Replace allocates the result on the host.
	growth := len(input.NewText) - len(input.OldText)
	if growth > 0 && growth > (state.MaxArtifactFileBytes-len(file.Content))/replacements {
		return EditFileOutput{}, fmt.Errorf("%w: edited content exceeds %d bytes", state.ErrInvalidArtifactFile, state.MaxArtifactFileBytes)
	}
	updatedContent := []byte(strings.Replace(string(file.Content), input.OldText, input.NewText, replacements))
	updated, revision, err := s.store.WriteArtifactFile(ctx, state.ArtifactFileWrite{
		ArtifactID:          input.ArtifactID,
		EditorPactSessionID: s.pactSessionID,
		Path:                file.Path,
		MediaType:           file.MediaType,
		Content:             updatedContent,
		ExpectedVersion:     file.Version,
	})
	return EditFileOutput{
		File:             updated,
		ArtifactRevision: revision,
		Replacements:     replacements,
	}, err
}

func decodeContent(encoding, content string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf-8", "utf8":
		if len(content) > state.MaxArtifactFileBytes {
			return nil, fmt.Errorf("%w: content exceeds %d bytes", state.ErrInvalidArtifactFile, state.MaxArtifactFileBytes)
		}
		if !utf8.ValidString(content) {
			return nil, errors.New("content is not valid UTF-8")
		}
		return []byte(content), nil
	case "base64":
		if len(content) > base64.StdEncoding.EncodedLen(state.MaxArtifactFileBytes) {
			return nil, fmt.Errorf("%w: encoded content exceeds the %d byte file limit", state.ErrInvalidArtifactFile, state.MaxArtifactFileBytes)
		}
		decoded, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, fmt.Errorf("decode base64 content: %w", err)
		}
		return decoded, nil
	default:
		return nil, errors.New("encoding must be utf-8 or base64")
	}
}

func encodeContent(mediaType string, content []byte) (string, string) {
	if textualMediaType(mediaType) && utf8.Valid(content) {
		return "utf-8", string(content)
	}
	return "base64", base64.StdEncoding.EncodeToString(content)
}

func textualMediaType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/ld+json", "application/javascript",
		"application/xml", "application/yaml", "application/toml":
		return true
	default:
		return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml")
	}
}

func normalizeMediaType(value, filePath string, content []byte) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = mime.TypeByExtension(path.Ext(filePath))
		if value == "" {
			value = http.DetectContentType(content)
		}
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return "", fmt.Errorf("invalid media_type: %w", err)
	}
	return mime.FormatMediaType(strings.ToLower(mediaType), parameters), nil
}
