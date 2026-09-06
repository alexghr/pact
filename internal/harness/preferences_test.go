package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/alexghr/pact/internal/state"
)

func TestPreferencesResolution(t *testing.T) {
	ctx := context.Background()
	store := openHarnessTestStore(t)
	if err := store.SaveModel(ctx, state.Model{Model: "custom", Name: "Custom", DefaultEffort: "high"}); err != nil {
		t.Fatal(err)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, m := range models {
		if m.Model == "custom" {
			id = m.ID
		}
	}
	if err := SavePreferences(ctx, store, state.Preferences{DefaultModelID: id, DefaultImageProfile: "go"}); err != nil {
		t.Fatal(err)
	}
	target := &state.ResumeTarget{Model: "removed-model", Effort: "low", DockerfileVariant: "generic"}
	for _, tt := range []struct {
		name                 string
		input                Options
		target               *state.ResumeTarget
		model, effort, image string
	}{
		{"preferences", Options{}, nil, "custom", "high", "go"},
		{"selected model", Options{Model: "gpt-6-astra"}, nil, "gpt-6-astra", "medium", "go"},
		{"explicit overrides", Options{Model: "custom", Effort: "max", Image: "generic"}, nil, "custom", "max", "generic"},
		{"resume removed model", Options{}, target, "removed-model", "low", "generic"},
		{"switch model", Options{Model: "custom"}, target, "custom", "high", "generic"},
		{"switch with effort", Options{Model: "custom", Effort: "max"}, target, "custom", "max", "generic"},
		{"unlisted model", Options{Model: "future"}, nil, "future", "", "go"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOptions(ctx, store, tt.input, tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if got.Model != tt.model || got.Effort != tt.effort || got.Image != tt.image {
				t.Fatalf("resolved = %#v", got)
			}
		})
	}
	for _, profile := range []string{"../private", "ubuntu:latest", ""} {
		err := SavePreferences(ctx, store, state.Preferences{DefaultModelID: id, DefaultImageProfile: profile})
		if !errors.Is(err, state.ErrInvalidSettings) {
			t.Fatalf("profile %q: %v", profile, err)
		}
	}
	p, err := store.GetPreferences(ctx)
	if err != nil || p.DefaultImageProfile != "go" {
		t.Fatalf("preferences changed after rejected update: %#v, %v", p, err)
	}
}
