package poster_test

import (
	"testing"

	"github.com/wmyers/heres-whats-happening/internal/poster"
)

// Two arbitrary but fixed user ids, so the tests below read as "same user" vs
// "different user" rather than as uuid noise.
const (
	userA = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	userB = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"
)

func TestJobIDIsDeterministic(t *testing.T) {
	a := poster.JobID(userA, "La Luz", "Neumos", "2026-08-20")
	b := poster.JobID(userA, "La Luz", "Neumos", "2026-08-20")
	if a != b {
		t.Errorf("JobID is not deterministic: %q != %q", a, b)
	}
}

func TestJobIDNormalizesCaseAndSurroundingWhitespace(t *testing.T) {
	a := poster.JobID(userA, "La Luz ", " Neumos", "2026-08-20")
	b := poster.JobID(userA, "la luz", "neumos ", "2026-08-20")
	if a != b {
		t.Errorf("JobID(%q, %q, ...) = %q, JobID(%q, %q, ...) = %q, want equal after normalization",
			"La Luz ", " Neumos", a, "la luz", "neumos ", b)
	}
}

func TestJobIDDistinctTriplesProduceDistinctIDs(t *testing.T) {
	a := poster.JobID(userA, "La Luz", "Neumos", "2026-08-20")
	b := poster.JobID(userA, "Khruangbin", "The Fillmore", "2026-08-15")
	if a == b {
		t.Errorf("JobID collided for distinct triples: both = %q", a)
	}
}

// Jobs are per user. Two users asking for the same show must get two rows:
// POST's force:true re-claims a ready row, so a shared id would let either of
// them blank the other's poster.
func TestJobIDIsScopedToTheUser(t *testing.T) {
	a := poster.JobID(userA, "La Luz", "Neumos", "2026-08-20")
	b := poster.JobID(userB, "La Luz", "Neumos", "2026-08-20")
	if a == b {
		t.Errorf("JobID ignored the user: both users got %q for the same show", a)
	}
}

// TestJobIDDoesNotCollideAcrossFieldBoundaries is a regression guard. JobID
// used to join fields with "\x00". A NUL byte is legal inside a JSON string,
// so it could be smuggled in through the request body and shift where one
// field ends and the next begins:
//
//	JobID(u, "foo\x00bar", "baz", "d") == JobID(u, "foo", "bar\x00baz", "d")
//
// Two distinct natural keys collapsing onto one job id means one poster gets
// served for a different show — or, now that the user is part of the key, for
// a different user. The boundary between user and performer is checked too:
// that one is the difference between "my poster" and "yours".
func TestJobIDDoesNotCollideAcrossFieldBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name       string
		aKey, bKey [4]string
	}{
		{
			name: "performer/venue",
			aKey: [4]string{userA, "foo\x00bar", "baz", "d"},
			bKey: [4]string{userA, "foo", "bar\x00baz", "d"},
		},
		{
			name: "venue/date",
			aKey: [4]string{userA, "p", "foo\x00bar", "baz"},
			bKey: [4]string{userA, "p", "foo", "bar\x00baz"},
		},
		{
			name: "user/performer",
			aKey: [4]string{userA + "\x00foo", "bar", "v", "d"},
			bKey: [4]string{userA, "foo\x00bar", "v", "d"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := poster.JobID(tc.aKey[0], tc.aKey[1], tc.aKey[2], tc.aKey[3])
			b := poster.JobID(tc.bKey[0], tc.bKey[1], tc.bKey[2], tc.bKey[3])
			if a == b {
				t.Errorf("JobID collided across the %s boundary: JobID%v == JobID%v == %q",
					tc.name, tc.aKey, tc.bKey, a)
			}
		})
	}
}
