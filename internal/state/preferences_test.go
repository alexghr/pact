package state

import (
	"context"
	"errors"
	"testing"
)

func TestModelCatalogEditsAndMigration(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	p, err := store.GetPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteModel(ctx, p.DefaultModelID); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("delete default = %v", err)
	}
	if err := store.SetPreferences(ctx, Preferences{DefaultModelID: 9999, DefaultImageProfile: "go"}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("missing default = %v", err)
	}
	if err := store.SaveModel(ctx, Model{Model: "custom", Name: "Custom", DefaultEffort: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveModel(ctx, Model{Model: "custom", Name: "Duplicate", DefaultEffort: "low"}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("duplicate = %v", err)
	}
	models, err := store.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var custom Model
	var removedID int64
	for _, m := range models {
		if m.Model == "custom" {
			custom = m
		}
		if m.Model == "gpt-6-astra" {
			removedID = m.ID
		}
	}
	custom.Name = "Edited"
	custom.DefaultEffort = "max"
	if err := store.SaveModel(ctx, custom); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteModel(ctx, removedID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPreferences(ctx, Preferences{DefaultModelID: custom.ID, DefaultImageProfile: "go"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteModel(ctx, p.DefaultModelID); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	models, err = store.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range models {
		if m.ID == removedID || m.ID == p.DefaultModelID {
			t.Fatal("migration resurrected removed model")
		}
		if m.ID == custom.ID {
			found = true
			if m.Name != "Edited" || m.DefaultEffort != "max" {
				t.Fatalf("edit lost: %#v", m)
			}
		}
	}
	if !found {
		t.Fatal("custom model lost")
	}
	p, err = store.GetPreferences(ctx)
	if err != nil || p.DefaultModelID != custom.ID || p.DefaultImageProfile != "go" {
		t.Fatalf("preferences = %#v, %v", p, err)
	}
}
