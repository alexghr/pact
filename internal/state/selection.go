package state

import (
	"fmt"
)

// LastThread selects the last used saved user conversation from native metadata.
// Unavailable threads participate in ordering so their absence cannot silently
// redirect a resume request to an older conversation.
func LastThread(threads []Thread) (Thread, error) {
	if len(threads) == 0 {
		return Thread{}, nil
	}
	var selected Thread
	for _, thread := range threads {
		if thread.Internal {
			continue
		}
		if selected.ID == "" || thread.LastUsed > selected.LastUsed ||
			(thread.LastUsed == selected.LastUsed && thread.ID > selected.ID) {
			selected = thread
		}
	}
	if selected.ID == "" {
		return Thread{}, fmt.Errorf("session contains only internal threads")
	}
	if selected.Archived {
		return Thread{}, fmt.Errorf("last used thread %s is archived", selected.ID)
	}
	if selected.Missing {
		return Thread{}, fmt.Errorf("last used thread %s is missing from native history", selected.ID)
	}
	return selected, nil
}
