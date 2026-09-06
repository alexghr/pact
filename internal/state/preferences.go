package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

var ErrInvalidSettings = errors.New("invalid settings")
var ErrModelNotFound = errors.New("model not found")

type Model struct {
	ID            int64
	Model         string
	Name          string
	DefaultEffort string
}

type Preferences struct {
	DefaultModelID      int64
	DefaultImageProfile string
}

func (s *Store) ListModels(ctx context.Context) ([]Model, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, model, name, default_effort FROM models ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()
	var models []Model
	for rows.Next() {
		var m Model
		if err := rows.Scan(&m.ID, &m.Model, &m.Name, &m.DefaultEffort); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

func (s *Store) SaveModel(ctx context.Context, m Model) error {
	m.Model = strings.TrimSpace(m.Model)
	m.Name = strings.TrimSpace(m.Name)
	m.DefaultEffort = strings.TrimSpace(m.DefaultEffort)
	if m.Model == "" || m.Name == "" || m.DefaultEffort == "" || strings.ContainsFunc(m.Model+m.DefaultEffort, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("%w: model identifier, name and default effort are required; identifiers and effort cannot contain whitespace", ErrInvalidSettings)
	}
	// Keep the identifier and effort extensible; Codex validates model capabilities.
	var result sql.Result
	var err error
	if m.ID == 0 {
		result, err = s.db.ExecContext(ctx, `INSERT INTO models (model, name, default_effort) VALUES (?, ?, ?)`, m.Model, m.Name, m.DefaultEffort)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE models SET model = ?, name = ?, default_effort = ? WHERE id = ?`, m.Model, m.Name, m.DefaultEffort, m.ID)
	}
	if err != nil {
		var exists bool
		if lookupErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM models WHERE model = ? AND id != ?)`, m.Model, m.ID).Scan(&exists); lookupErr == nil && exists {
			return fmt.Errorf("%w: model identifier already exists", ErrInvalidSettings)
		}
		return fmt.Errorf("save model: %w", err)
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrModelNotFound
	}
	return err
}

func (s *Store) DeleteModel(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM models WHERE id = ? AND id NOT IN (SELECT default_model_id FROM preferences)`, id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("%w: model missing or is the global default; choose a replacement first", ErrInvalidSettings)
	}
	return err
}

func (s *Store) GetPreferences(ctx context.Context) (Preferences, error) {
	var p Preferences
	err := s.db.QueryRowContext(ctx, `SELECT default_model_id, default_image_profile FROM preferences WHERE id = 1`).Scan(&p.DefaultModelID, &p.DefaultImageProfile)
	return p, err
}

// SetPreferences persists preferences after the host policy layer validates the profile.
func (s *Store) SetPreferences(ctx context.Context, p Preferences) error {
	result, err := s.db.ExecContext(ctx, `UPDATE preferences SET default_model_id = ?, default_image_profile = ? WHERE id = 1 AND EXISTS (SELECT 1 FROM models WHERE id = ?)`, p.DefaultModelID, p.DefaultImageProfile, p.DefaultModelID)
	if err != nil {
		return fmt.Errorf("save preferences: %w", err)
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("%w: default model does not exist", ErrInvalidSettings)
	}
	return err
}
