package state

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	MaxArtifactNameBytes        = 200
	MaxArtifactDescriptionBytes = 4000
	MaxArtifactPathBytes        = 1024
	MaxArtifactFileBytes        = 16 << 20
)

var (
	ErrArtifactNotFound     = errors.New("artifact not found")
	ErrArtifactFileNotFound = errors.New("artifact file not found")
	ErrArtifactConflict     = errors.New("artifact version conflict")
	ErrInvalidArtifact      = errors.New("invalid artifact")
	ErrInvalidArtifactFile  = errors.New("invalid artifact file")
)

type Artifact struct {
	ID                     int64          `json:"id"`
	CreatorPactSessionID   int64          `json:"creator_pact_session_id"`
	UpdatedByPactSessionID int64          `json:"updated_by_pact_session_id"`
	Name                   string         `json:"name"`
	Description            string         `json:"description"`
	Revision               int64          `json:"revision"`
	CreatedAt              string         `json:"created_at"`
	UpdatedAt              string         `json:"updated_at"`
	Files                  []ArtifactFile `json:"files"`
}

type ArtifactSummary struct {
	ID                     int64  `json:"id"`
	CreatorPactSessionID   int64  `json:"creator_pact_session_id"`
	UpdatedByPactSessionID int64  `json:"updated_by_pact_session_id"`
	Name                   string `json:"name"`
	Description            string `json:"description"`
	Revision               int64  `json:"revision"`
	FileCount              int64  `json:"file_count"`
	SizeBytes              int64  `json:"size_bytes"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type ArtifactFile struct {
	ArtifactID             int64  `json:"artifact_id"`
	Path                   string `json:"path"`
	MediaType              string `json:"media_type"`
	SizeBytes              int64  `json:"size_bytes"`
	SHA256                 string `json:"sha256"`
	Version                int64  `json:"version"`
	UpdatedByPactSessionID int64  `json:"updated_by_pact_session_id"`
	CreatedAt              string `json:"created_at"`
	UpdatedAt              string `json:"updated_at"`
}

type ArtifactFileContent struct {
	ArtifactFile
	Content []byte `json:"-"`
}

type ArtifactFileChunk struct {
	ArtifactFile
	Content []byte `json:"-"`
	Offset  int64  `json:"offset"`
}

type ArtifactSearch struct {
	Query                string
	CreatorPactSessionID int64
	Limit                int
	Offset               int
}

type ArtifactPage struct {
	Artifacts  []ArtifactSummary `json:"artifacts"`
	NextOffset *int              `json:"next_offset,omitempty"`
}

type ArtifactUpdate struct {
	ID                  int64
	EditorPactSessionID int64
	Name                *string
	Description         *string
	ExpectedRevision    int64
}

type ArtifactFileWrite struct {
	ArtifactID          int64
	EditorPactSessionID int64
	Path                string
	MediaType           string
	Content             []byte
	ExpectedVersion     int64
}

func (s *Store) CreateArtifact(ctx context.Context, creatorSessionID int64, name, description string) (Artifact, error) {
	name, description, err := normalizeArtifactMetadata(name, description)
	if err != nil {
		return Artifact{}, err
	}
	artifact, err := scanArtifact(s.db.QueryRowContext(ctx, `
		INSERT INTO artifacts (
			creator_pact_session_id,
			updated_by_pact_session_id,
			name,
			description
		)
		SELECT ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM pact_sessions WHERE id = ?)
		RETURNING id, creator_pact_session_id, updated_by_pact_session_id,
			name, description, revision, created_at, updated_at`,
		creatorSessionID,
		creatorSessionID,
		name,
		description,
		creatorSessionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("create artifact: session %d: %w", creatorSessionID, ErrSessionNotFound)
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("create artifact: %w", err)
	}
	artifact.Files = []ArtifactFile{}
	return artifact, nil
}

func (s *Store) SearchArtifacts(ctx context.Context, search ArtifactSearch) (ArtifactPage, error) {
	if search.CreatorPactSessionID < 0 || search.Offset < 0 {
		return ArtifactPage{}, fmt.Errorf("%w: session filter and offset must be non-negative", ErrInvalidArtifact)
	}
	if search.Limit < 0 || search.Limit > 100 {
		return ArtifactPage{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidArtifact)
	}
	if search.Limit == 0 {
		search.Limit = 50
	}
	search.Query = strings.TrimSpace(search.Query)

	rows, err := s.db.QueryContext(ctx, `
		SELECT artifact.id, artifact.creator_pact_session_id,
			artifact.updated_by_pact_session_id, artifact.name,
			artifact.description, artifact.revision,
			COUNT(file.path), COALESCE(SUM(file.size_bytes), 0),
			artifact.created_at, artifact.updated_at
		FROM artifacts AS artifact
		LEFT JOIN artifact_files AS file ON file.artifact_id = artifact.id
		WHERE (? = '' OR instr(lower(artifact.name), lower(?)) > 0
			OR instr(lower(artifact.description), lower(?)) > 0
			OR EXISTS (
				SELECT 1 FROM artifact_files AS matching_file
				WHERE matching_file.artifact_id = artifact.id
					AND instr(lower(matching_file.path), lower(?)) > 0
			))
			AND (? = 0 OR artifact.creator_pact_session_id = ?)
		GROUP BY artifact.id
		ORDER BY artifact.id DESC
		LIMIT ? OFFSET ?`,
		search.Query,
		search.Query,
		search.Query,
		search.Query,
		search.CreatorPactSessionID,
		search.CreatorPactSessionID,
		search.Limit+1,
		search.Offset,
	)
	if err != nil {
		return ArtifactPage{}, fmt.Errorf("search artifacts: %w", err)
	}
	defer rows.Close()

	artifacts := make([]ArtifactSummary, 0)
	for rows.Next() {
		var artifact ArtifactSummary
		if err := rows.Scan(
			&artifact.ID,
			&artifact.CreatorPactSessionID,
			&artifact.UpdatedByPactSessionID,
			&artifact.Name,
			&artifact.Description,
			&artifact.Revision,
			&artifact.FileCount,
			&artifact.SizeBytes,
			&artifact.CreatedAt,
			&artifact.UpdatedAt,
		); err != nil {
			return ArtifactPage{}, fmt.Errorf("search artifacts: scan row: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return ArtifactPage{}, fmt.Errorf("search artifacts: read rows: %w", err)
	}
	page := ArtifactPage{Artifacts: artifacts}
	if len(artifacts) > search.Limit {
		page.Artifacts = artifacts[:search.Limit]
		next := search.Offset + search.Limit
		page.NextOffset = &next
	}
	return page, nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID int64) (Artifact, error) {
	artifact, err := scanArtifact(s.db.QueryRowContext(ctx, `
		SELECT id, creator_pact_session_id, updated_by_pact_session_id,
			name, description, revision, created_at, updated_at
		FROM artifacts
		WHERE id = ?`, artifactID))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fmt.Errorf("get artifact %d: %w", artifactID, ErrArtifactNotFound)
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("get artifact %d: %w", artifactID, err)
	}

	files, err := s.listArtifactFiles(ctx, artifactID)
	if err != nil {
		return Artifact{}, err
	}
	artifact.Files = files
	return artifact, nil
}

func (s *Store) UpdateArtifact(ctx context.Context, update ArtifactUpdate) (Artifact, error) {
	if update.Name == nil && update.Description == nil {
		return Artifact{}, fmt.Errorf("update artifact: %w: name or description is required", ErrInvalidArtifact)
	}
	if update.ExpectedRevision <= 0 {
		return Artifact{}, fmt.Errorf("update artifact: %w: expected revision must be positive", ErrInvalidArtifact)
	}
	var name any
	if update.Name != nil {
		normalized, _, err := normalizeArtifactMetadata(*update.Name, "")
		if err != nil {
			return Artifact{}, err
		}
		name = normalized
	}
	var description any
	if update.Description != nil {
		normalized, err := normalizeArtifactDescription(*update.Description)
		if err != nil {
			return Artifact{}, err
		}
		description = normalized
	}
	artifact, err := scanArtifact(s.db.QueryRowContext(ctx, `
		UPDATE artifacts
		SET name = COALESCE(?, name),
			description = COALESCE(?, description),
			updated_by_pact_session_id = ?,
			revision = revision + 1,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ? AND revision = ?
		RETURNING id, creator_pact_session_id, updated_by_pact_session_id,
			name, description, revision, created_at, updated_at`,
		name,
		description,
		update.EditorPactSessionID,
		update.ID,
		update.ExpectedRevision,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, s.artifactMutationMiss(ctx, "update artifact", update.ID)
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("update artifact %d: %w", update.ID, err)
	}
	files, err := s.listArtifactFiles(ctx, update.ID)
	if err != nil {
		return Artifact{}, err
	}
	artifact.Files = files
	return artifact, nil
}

func (s *Store) WriteArtifactFile(ctx context.Context, write ArtifactFileWrite) (ArtifactFile, int64, error) {
	filePath, err := NormalizeArtifactFilePath(write.Path)
	if err != nil {
		return ArtifactFile{}, 0, err
	}
	if write.MediaType == "" {
		return ArtifactFile{}, 0, fmt.Errorf("%w: media type must not be empty", ErrInvalidArtifactFile)
	}
	if write.ExpectedVersion < 0 {
		return ArtifactFile{}, 0, fmt.Errorf("%w: expected version must be non-negative", ErrInvalidArtifactFile)
	}
	if len(write.Content) > MaxArtifactFileBytes {
		return ArtifactFile{}, 0, fmt.Errorf("%w: content exceeds %d bytes", ErrInvalidArtifactFile, MaxArtifactFileBytes)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(write.Content))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArtifactFile{}, 0, fmt.Errorf("write artifact file: begin: %w", err)
	}
	defer tx.Rollback()

	var artifactRevision int64
	err = tx.QueryRowContext(ctx, `
		UPDATE artifacts
		SET updated_by_pact_session_id = ?, revision = revision + 1,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = ?
		RETURNING revision`, write.EditorPactSessionID, write.ArtifactID).Scan(&artifactRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactFile{}, 0, fmt.Errorf("write artifact file: artifact %d: %w", write.ArtifactID, ErrArtifactNotFound)
	}
	if err != nil {
		return ArtifactFile{}, 0, fmt.Errorf("write artifact file: update artifact: %w", err)
	}

	write.Path = filePath
	file, err := writeArtifactFileRow(ctx, tx, write, digest)
	if err != nil {
		return ArtifactFile{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return ArtifactFile{}, 0, fmt.Errorf("write artifact file: commit: %w", err)
	}
	return file, artifactRevision, nil
}

func writeArtifactFileRow(
	ctx context.Context,
	tx *sql.Tx,
	write ArtifactFileWrite,
	digest string,
) (ArtifactFile, error) {
	const returning = `
		RETURNING artifact_id, path, media_type, size_bytes, sha256,
			version, updated_by_pact_session_id, created_at, updated_at`
	var row *sql.Row
	if write.ExpectedVersion == 0 {
		row = tx.QueryRowContext(ctx, `
			INSERT INTO artifact_files (
				artifact_id, path, media_type, content, size_bytes, sha256,
				updated_by_pact_session_id
			)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (artifact_id, path) DO NOTHING`+returning,
			write.ArtifactID,
			write.Path,
			write.MediaType,
			write.Content,
			len(write.Content),
			digest,
			write.EditorPactSessionID,
		)
	} else {
		row = tx.QueryRowContext(ctx, `
			UPDATE artifact_files
			SET media_type = ?, content = ?, size_bytes = ?, sha256 = ?,
				version = version + 1, updated_by_pact_session_id = ?,
				updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			WHERE artifact_id = ? AND path = ? AND version = ?`+returning,
			write.MediaType,
			write.Content,
			len(write.Content),
			digest,
			write.EditorPactSessionID,
			write.ArtifactID,
			write.Path,
			write.ExpectedVersion,
		)
	}

	file, err := scanArtifactFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactFile{}, fmt.Errorf(
			"write artifact file %d/%s: %w",
			write.ArtifactID,
			write.Path,
			ErrArtifactConflict,
		)
	}
	if err != nil {
		return ArtifactFile{}, fmt.Errorf("write artifact file %d/%s: %w", write.ArtifactID, write.Path, err)
	}
	return file, nil
}

func (s *Store) ReadArtifactFile(
	ctx context.Context,
	artifactID int64,
	filePath string,
	offset int64,
	limit int64,
) (ArtifactFileChunk, error) {
	filePath, err := NormalizeArtifactFilePath(filePath)
	if err != nil {
		return ArtifactFileChunk{}, err
	}
	if offset < 0 || limit <= 0 {
		return ArtifactFileChunk{}, fmt.Errorf("%w: offset must be non-negative and limit must be positive", ErrInvalidArtifactFile)
	}

	var chunk ArtifactFileChunk
	chunk.Offset = offset
	err = s.db.QueryRowContext(ctx, `
		SELECT artifact_id, path, media_type, size_bytes, sha256,
			version, updated_by_pact_session_id, created_at, updated_at,
			substr(content, ?, ?)
		FROM artifact_files
		WHERE artifact_id = ? AND path = ?`,
		offset+1,
		limit,
		artifactID,
		filePath,
	).Scan(
		&chunk.ArtifactID,
		&chunk.Path,
		&chunk.MediaType,
		&chunk.SizeBytes,
		&chunk.SHA256,
		&chunk.Version,
		&chunk.UpdatedByPactSessionID,
		&chunk.CreatedAt,
		&chunk.UpdatedAt,
		&chunk.Content,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactFileChunk{}, fmt.Errorf("read artifact file %d/%s: %w", artifactID, filePath, ErrArtifactFileNotFound)
	}
	if err != nil {
		return ArtifactFileChunk{}, fmt.Errorf("read artifact file %d/%s: %w", artifactID, filePath, err)
	}
	return chunk, nil
}

func (s *Store) GetArtifactFile(ctx context.Context, artifactID int64, filePath string) (ArtifactFileContent, error) {
	filePath, err := NormalizeArtifactFilePath(filePath)
	if err != nil {
		return ArtifactFileContent{}, err
	}
	var file ArtifactFileContent
	err = s.db.QueryRowContext(ctx, `
		SELECT artifact_id, path, media_type, size_bytes, sha256,
			version, updated_by_pact_session_id, created_at, updated_at, content
		FROM artifact_files
		WHERE artifact_id = ? AND path = ?`, artifactID, filePath).Scan(
		&file.ArtifactID,
		&file.Path,
		&file.MediaType,
		&file.SizeBytes,
		&file.SHA256,
		&file.Version,
		&file.UpdatedByPactSessionID,
		&file.CreatedAt,
		&file.UpdatedAt,
		&file.Content,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactFileContent{}, fmt.Errorf("get artifact file %d/%s: %w", artifactID, filePath, ErrArtifactFileNotFound)
	}
	if err != nil {
		return ArtifactFileContent{}, fmt.Errorf("get artifact file %d/%s: %w", artifactID, filePath, err)
	}
	return file, nil
}

func (s *Store) listArtifactFiles(ctx context.Context, artifactID int64) ([]ArtifactFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT artifact_id, path, media_type, size_bytes, sha256,
			version, updated_by_pact_session_id, created_at, updated_at
		FROM artifact_files
		WHERE artifact_id = ?
		ORDER BY path`, artifactID)
	if err != nil {
		return nil, fmt.Errorf("list files for artifact %d: %w", artifactID, err)
	}
	defer rows.Close()

	files := make([]ArtifactFile, 0)
	for rows.Next() {
		file, err := scanArtifactFile(rows)
		if err != nil {
			return nil, fmt.Errorf("list files for artifact %d: scan row: %w", artifactID, err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list files for artifact %d: read rows: %w", artifactID, err)
	}
	return files, nil
}

func (s *Store) artifactMutationMiss(ctx context.Context, operation string, artifactID int64) error {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM artifacts WHERE id = ?`, artifactID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s %d: %w", operation, artifactID, ErrArtifactNotFound)
	}
	if err != nil {
		return fmt.Errorf("%s %d: %w", operation, artifactID, err)
	}
	return fmt.Errorf("%s %d: %w", operation, artifactID, ErrArtifactConflict)
}

func NormalizeArtifactFilePath(filePath string) (string, error) {
	if filePath == "" || len(filePath) > MaxArtifactPathBytes || !utf8.ValidString(filePath) {
		return "", fmt.Errorf("%w: path must be valid UTF-8 between 1 and %d bytes", ErrInvalidArtifactFile, MaxArtifactPathBytes)
	}
	if strings.ContainsRune(filePath, '\\') || path.IsAbs(filePath) {
		return "", fmt.Errorf("%w: path must be a relative slash-separated path", ErrInvalidArtifactFile)
	}
	for _, character := range filePath {
		if character < 0x20 || character == 0x7f {
			return "", fmt.Errorf("%w: path must not contain control characters", ErrInvalidArtifactFile)
		}
	}
	cleaned := path.Clean(filePath)
	if cleaned == "." || cleaned == ".." || cleaned != filePath || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: path must be normalized and must not contain '.' or '..' segments", ErrInvalidArtifactFile)
	}
	return cleaned, nil
}

func normalizeArtifactMetadata(name, description string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", fmt.Errorf("%w: name must not be empty", ErrInvalidArtifact)
	}
	if !utf8.ValidString(name) || len(name) > MaxArtifactNameBytes {
		return "", "", fmt.Errorf("%w: name must be valid UTF-8 and at most %d bytes", ErrInvalidArtifact, MaxArtifactNameBytes)
	}
	description, err := normalizeArtifactDescription(description)
	return name, description, err
}

func normalizeArtifactDescription(description string) (string, error) {
	description = strings.TrimSpace(description)
	if !utf8.ValidString(description) || len(description) > MaxArtifactDescriptionBytes {
		return "", fmt.Errorf("%w: description must be valid UTF-8 and at most %d bytes", ErrInvalidArtifact, MaxArtifactDescriptionBytes)
	}
	return description, nil
}

func scanArtifact(row scanner) (Artifact, error) {
	var artifact Artifact
	err := row.Scan(
		&artifact.ID,
		&artifact.CreatorPactSessionID,
		&artifact.UpdatedByPactSessionID,
		&artifact.Name,
		&artifact.Description,
		&artifact.Revision,
		&artifact.CreatedAt,
		&artifact.UpdatedAt,
	)
	return artifact, err
}

func scanArtifactFile(row scanner) (ArtifactFile, error) {
	var file ArtifactFile
	err := row.Scan(
		&file.ArtifactID,
		&file.Path,
		&file.MediaType,
		&file.SizeBytes,
		&file.SHA256,
		&file.Version,
		&file.UpdatedByPactSessionID,
		&file.CreatedAt,
		&file.UpdatedAt,
	)
	return file, err
}
