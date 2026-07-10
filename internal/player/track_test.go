package player

import "testing"

func TestQueueReplaceTrackReplacesCurrentTrack(t *testing.T) {
	queue := NewQueue()
	current := &Track{Title: "current"}
	next := &Track{Title: "next"}
	replacement := &Track{Title: "replacement"}

	queue.Add(current)
	queue.Add(next)
	if got := queue.Next(); got != current {
		t.Fatalf("Next() = %p, want %p", got, current)
	}

	if ok := queue.ReplaceTrack(current, replacement); !ok {
		t.Fatal("ReplaceTrack() = false, want true")
	}

	if got := queue.Current(); got != replacement {
		t.Fatalf("Current() = %p, want %p", got, replacement)
	}

	index, isCurrent, ok := queue.FindTrack(replacement)
	if !ok || index != 0 || !isCurrent {
		t.Fatalf("FindTrack() = (%d, %v, %v), want (0, true, true)", index, isCurrent, ok)
	}
}

func TestQueueReplaceTrackReplacesUpcomingTrack(t *testing.T) {
	queue := NewQueue()
	current := &Track{Title: "current"}
	upcoming := &Track{Title: "upcoming"}
	replacement := &Track{Title: "replacement"}

	queue.Add(current)
	queue.Add(upcoming)
	queue.Next()

	if ok := queue.ReplaceTrack(upcoming, replacement); !ok {
		t.Fatal("ReplaceTrack() = false, want true")
	}

	if got := queue.Peek(); got != replacement {
		t.Fatalf("Peek() = %p, want %p", got, replacement)
	}

	index, isCurrent, ok := queue.FindTrack(replacement)
	if !ok || index != 1 || isCurrent {
		t.Fatalf("FindTrack() = (%d, %v, %v), want (1, false, true)", index, isCurrent, ok)
	}
}

func TestQueueRemoveTrackRemovesCurrentTrack(t *testing.T) {
	queue := NewQueue()
	first := &Track{Title: "first"}
	current := &Track{Title: "current"}
	next := &Track{Title: "next"}

	queue.Add(first)
	queue.Add(current)
	queue.Add(next)
	queue.Next()
	queue.Next()

	ok, wasCurrent := queue.RemoveTrack(current)
	if !ok {
		t.Fatal("RemoveTrack() = false, want true")
	}
	if !wasCurrent {
		t.Fatal("RemoveTrack() wasCurrent = false, want true")
	}

	tracks, currentIndex := queue.Snapshot()
	if len(tracks) != 2 {
		t.Fatalf("len(Snapshot()) = %d, want 2", len(tracks))
	}
	// There is deliberately no current entry until the stopped session has
	// transitioned. This keeps loop mode from replaying the prior track.
	if currentIndex != -1 {
		t.Fatalf("CurrentIndex = %d, want -1 while current removal is pending", currentIndex)
	}
	if !queue.HasPendingCurrentRemoval() {
		t.Fatal("HasPendingCurrentRemoval() = false, want true")
	}
	if got := queue.Current(); got != nil {
		t.Fatalf("Current() = %p, want nil while current removal is pending", got)
	}
	if got := queue.TryAdvance(); got != next {
		t.Fatalf("TryAdvance() = %p, want immediate successor %p", got, next)
	}
	if queue.HasPendingCurrentRemoval() {
		t.Fatal("HasPendingCurrentRemoval() remained true after transition")
	}
}

func TestQueueRemoveRemovesCurrentTrack(t *testing.T) {
	queue := NewQueue()
	first := &Track{Title: "first"}
	current := &Track{Title: "current"}
	next := &Track{Title: "next"}

	queue.Add(first)
	queue.Add(current)
	queue.Add(next)
	queue.Next()
	queue.Next()

	ok, wasCurrent := queue.Remove(1)
	if !ok {
		t.Fatal("Remove(1) = false, want true")
	}
	if !wasCurrent {
		t.Fatal("Remove(1) wasCurrent = false, want true")
	}

	tracks, currentIndex := queue.Snapshot()
	if len(tracks) != 2 {
		t.Fatalf("len(Snapshot()) = %d, want 2", len(tracks))
	}
	if currentIndex != -1 {
		t.Fatalf("CurrentIndex = %d, want -1 while current removal is pending", currentIndex)
	}
	if got := queue.Current(); got != nil {
		t.Fatalf("Current() = %p, want nil while current removal is pending", got)
	}
	if got := queue.TryAdvance(); got != next {
		t.Fatalf("TryAdvance() = %p, want immediate successor %p", got, next)
	}
}

func TestQueueRemoveTrackRemovesUpcomingTrack(t *testing.T) {
	queue := NewQueue()
	current := &Track{Title: "current"}
	removeMe := &Track{Title: "remove-me"}
	next := &Track{Title: "next"}

	queue.Add(current)
	queue.Add(removeMe)
	queue.Add(next)
	queue.Next()

	ok, wasCurrent := queue.RemoveTrack(removeMe)
	if !ok {
		t.Fatal("RemoveTrack() = false, want true")
	}
	if wasCurrent {
		t.Fatal("RemoveTrack() wasCurrent = true, want false")
	}

	tracks, currentIndex := queue.Snapshot()
	if len(tracks) != 2 {
		t.Fatalf("len(Snapshot()) = %d, want 2", len(tracks))
	}
	if currentIndex != 0 {
		t.Fatalf("CurrentIndex = %d, want 0", currentIndex)
	}
	if got := queue.Peek(); got != next {
		t.Fatalf("Peek() = %p, want %p", got, next)
	}
}

func TestQueueCurrentRemovalBypassesLoopAndSelectsImmediateSuccessor(t *testing.T) {
	queue := NewQueue()
	first := &Track{Title: "first"}
	current := &Track{Title: "current"}
	next := &Track{Title: "next"}
	queue.Add(first)
	queue.Add(current)
	queue.Add(next)
	queue.Next()
	queue.Next()
	queue.ToggleLoop()

	if ok, wasCurrent := queue.RemoveTrack(current); !ok || !wasCurrent {
		t.Fatalf("RemoveTrack() = (%v, %v), want (true, true)", ok, wasCurrent)
	}
	if got := queue.Next(); got != next {
		t.Fatalf("Next() after current removal with loop enabled = %p, want successor %p", got, next)
	}
	if got := queue.Current(); got != next {
		t.Fatalf("Current() after transition = %p, want %p", got, next)
	}
}

func TestQueueRewindCurrentDoesNotReplayPriorTrack(t *testing.T) {
	queue := NewQueue()
	first := &Track{Title: "first"}
	second := &Track{Title: "second"}
	queue.Add(first)
	queue.Add(second)
	queue.Next()
	queue.Next()

	queue.RewindCurrent()
	if got := queue.Current(); got != second {
		t.Fatalf("Current() after RewindCurrent() = %p, want current track %p", got, second)
	}
}

func TestQueueMoveRejectsCurrentSlotAndMovesUpcomingTracks(t *testing.T) {
	queue := NewQueue()
	current := &Track{Title: "current"}
	first := &Track{Title: "first"}
	second := &Track{Title: "second"}
	queue.Add(current)
	queue.Add(first)
	queue.Add(second)
	queue.Next()

	if queue.Move(0, 1) || queue.Move(1, 0) {
		t.Fatal("Move() allowed moving the active current slot")
	}
	if !queue.Move(1, 2) {
		t.Fatal("Move() rejected an upcoming-only move")
	}
	if got := queue.Peek(); got != second {
		t.Fatalf("Peek() after upcoming move = %p, want %p", got, second)
	}
}
