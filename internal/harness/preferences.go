package harness

import (
	"context"
	"fmt"

	"github.com/alexghr/pact/internal/imagebuilder"
	"github.com/alexghr/pact/internal/state"
)

func SavePreferences(ctx context.Context, store *state.Store, p state.Preferences) error {
	for _, profile := range imagebuilder.BuiltinProfiles() {
		if profile.Name == p.DefaultImageProfile {
			return store.SetPreferences(ctx, p)
		}
	}
	return fmt.Errorf("%w: unsupported image profile", state.ErrInvalidSettings)
}

// ResolveOptions applies explicit overrides, recorded thread settings, then system preferences.
// Unlisted explicit models remain usable with an explicit effort (or Codex's own default).
func ResolveOptions(ctx context.Context, store *state.Store, options Options, target *state.ResumeTarget) (Options, error) {
	if target != nil {
		if options.Model == "" || options.Model == target.Model {
			options.Model = target.Model
			if options.Effort == "" {
				options.Effort = target.Effort
			}
		}
		if options.Image == "" {
			options.Image = target.DockerfileVariant
		}
	}
	if options.Model != "" && options.Effort != "" && options.Image != "" {
		return options, nil
	}
	p, err := store.GetPreferences(ctx)
	if err != nil {
		return Options{}, fmt.Errorf("read preferences: %w", err)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		return Options{}, err
	}
	if options.Image == "" {
		options.Image = p.DefaultImageProfile
	}
	for _, m := range models {
		if options.Model == "" && m.ID == p.DefaultModelID {
			options.Model = m.Model
			break
		}
	}
	for _, m := range models {
		if m.Model == options.Model && options.Effort == "" {
			options.Effort = m.DefaultEffort
			break
		}
	}
	return options, nil
}
