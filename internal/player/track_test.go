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
	// CurrentIndex should stay at 1 so that the next track (which slid
	// into that slot) becomes the new current.
	if currentIndex != 1 {
		t.Fatalf("CurrentIndex = %d, want 1", currentIndex)
	}
	if got := queue.Current(); got != next {
		t.Fatalf("Current() = %p, want %p", got, next)
	}
	if got := queue.Peek(); got != nil {
		t.Fatalf("Peek() = %p, want nil", got)
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
	if currentIndex != 1 {
		t.Fatalf("CurrentIndex = %d, want 1", currentIndex)
	}
	if got := queue.Current(); got != next {
		t.Fatalf("Current() = %p, want %p", got, next)
	}
	if got := queue.Peek(); got != nil {
		t.Fatalf("Peek() = %p, want nil", got)
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
