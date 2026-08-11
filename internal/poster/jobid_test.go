package poster_test

import (
	"testing"

	"github.com/wmyers/heres-whats-happening/internal/poster"
)

func TestJobIDIsDeterministic(t *testing.T) {
	a := poster.JobID("La Luz", "Neumos", "2026-08-20")
	b := poster.JobID("La Luz", "Neumos", "2026-08-20")
	if a != b {
		t.Errorf("JobID is not deterministic: %q != %q", a, b)
	}
}

func TestJobIDNormalizesCaseAndSurroundingWhitespace(t *testing.T) {
	a := poster.JobID("La Luz ", " Neumos", "2026-08-20")
	b := poster.JobID("la luz", "neumos ", "2026-08-20")
	if a != b {
		t.Errorf("JobID(%q, %q, ...) = %q, JobID(%q, %q, ...) = %q, want equal after normalization",
			"La Luz ", " Neumos", a, "la luz", "neumos ", b)
	}
}

func TestJobIDDistinctTriplesProduceDistinctIDs(t *testing.T) {
	a := poster.JobID("La Luz", "Neumos", "2026-08-20")
	b := poster.JobID("Khruangbin", "The Fillmore", "2026-08-15")
	if a == b {
		t.Errorf("JobID collided for distinct triples: both = %q", a)
	}
}

// TestJobIDDoesNotCollideAcrossFieldBoundaries is a regression guard. JobID
// used to join fields with "\x00". A NUL byte is legal inside a JSON string,
// so it could be smuggled in through the request body and shift where one
// field ends and the next begins:
//
//	JobID("foo\x00bar", "baz", "d") == JobID("foo", "bar\x00baz", "d")
//
// Two distinct (performer, venue, date) triples collapsing onto one job id
// means one poster gets served for a different show.
func TestJobIDDoesNotCollideAcrossFieldBoundaries(t *testing.T) {
	a := poster.JobID("foo\x00bar", "baz", "d")
	b := poster.JobID("foo", "bar\x00baz", "d")
	if a == b {
		t.Errorf("JobID collided across a field boundary: JobID(%q,%q,%q) == JobID(%q,%q,%q) == %q",
			"foo\x00bar", "baz", "d", "foo", "bar\x00baz", "d", a)
	}
}
