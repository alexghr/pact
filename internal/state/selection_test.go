package state

import (
	"slices"
	"strings"
	"testing"
)

func TestLastThreadUsesLastUseWithStableTies(t *testing.T) {
	older := Thread{ID: "01a14eba-7c54-7170-96ec-8d86bb280eed", LastUsed: 10}
	newer := Thread{ID: "01a14eba-7c54-7170-96ec-8d86bb280eec", LastUsed: 20}
	internal := Thread{ID: "01a14eba-7c54-7170-96ec-8d86bb280eef", LastUsed: 30, Internal: true}
	threads := []Thread{newer, internal, older}
	for range 2 {
		selected, err := LastThread(threads)
		if err != nil || selected.ID != newer.ID {
			t.Fatalf("selection = %v, %v; want user conversation %s", selected, err, newer.ID)
		}
		slices.Reverse(threads)
	}
	older.LastUsed = newer.LastUsed
	threads = []Thread{older, newer}
	for range 2 {
		selected, err := LastThread(threads)
		if err != nil || selected.ID != older.ID {
			t.Fatalf("tie selection = %v, %v; want stable thread-ID tie-break", selected, err)
		}
		slices.Reverse(threads)
	}
}

func TestLastThreadDoesNotSubstituteAnotherConversation(t *testing.T) {
	available := Thread{ID: "01a14eba-7c54-7170-96ec-8d86bb280eec", LastUsed: 10}
	for _, scenario := range []struct {
		name    string
		other   Thread
		message string
	}{
		{"archived latest", Thread{LastUsed: 20, Archived: true}, "archived"},
		{"missing latest", Thread{LastUsed: 20, Missing: true}, "missing"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			scenario.other.ID = "01a14eba-7c54-7170-96ec-8d86bb280eed"
			selected, err := LastThread([]Thread{available, scenario.other})
			if selected.ID != "" || err == nil || !strings.Contains(err.Error(), scenario.message) {
				t.Fatalf("selection substituted a conversation: %v, %v", selected, err)
			}
		})
	}
	// An archived older conversation does not obstruct a known newer one.
	archived := available
	archived.ID = "01a14eba-7c54-7170-96ec-8d86bb280eed"
	archived.LastUsed = 1
	archived.Archived = true
	selected, err := LastThread([]Thread{archived, available})
	if err != nil || selected.ID != available.ID {
		t.Fatalf("older archive blocked current conversation: %v, %v", selected, err)
	}
}

func TestLastThreadDistinguishesEmptyAndInternalHistory(t *testing.T) {
	selected, err := LastThread(nil)
	if err != nil || selected.ID != "" {
		t.Fatalf("empty session cannot start its first conversation: %v, %v", selected, err)
	}
	selected, err = LastThread([]Thread{{ID: "01a14eba-7c54-7170-96ec-8d86bb280eec", Internal: true}})
	if err == nil || selected.ID != "" {
		t.Fatalf("internal history was mistaken for a new session: %v, %v", selected, err)
	}
}
