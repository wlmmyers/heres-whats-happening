package events_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

func TestContractFixtures_UnmarshalIntoMessage(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "event-message-contract")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	ran := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)

			var m events.Message
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields() // catches a TS field the Go struct lacks
			require.NoError(t, dec.Decode(&m))

			require.NotEmpty(t, m.SourceID)
			require.NotEmpty(t, m.SourceEventID)
			require.NotEmpty(t, m.Title)
			require.False(t, m.StartsAt.IsZero(), "starts_at must parse")
			require.NotEmpty(t, m.Venue.Name)
			if m.EndsAt != nil {
				require.False(t, m.EndsAt.IsZero(), "ends_at must parse when present")
			}
		})
	}
	require.Positive(t, ran, "no .json fixtures found in %s", dir)
}
