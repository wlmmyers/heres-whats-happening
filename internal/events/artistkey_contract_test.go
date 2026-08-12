// internal/events/artistkey_contract_test.go
package events_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

// The Lambda's artistKey() must produce byte-identical output to
// NormalizeString, because the Lambda keys its S3 skip cache on one and the
// database keys artists.name_key on the other. This fixture is asserted by
// BOTH this test and lambda/mastra-handler/src/artist-key.test.ts; a change to
// either implementation that is not mirrored fails on one side.
//
// Note this is deliberately NOT hash.ts's normalize(), which additionally
// strips punctuation and must not change (it feeds source_event_id).
func TestArtistKeyContract_MatchesNormalizeString(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "artist-key-contract", "cases.json"))
	require.NoError(t, err)

	var cases []struct {
		In  string `json:"in"`
		Out string `json:"out"`
		Why string `json:"why"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.NotEmpty(t, cases)

	for _, c := range cases {
		t.Run(c.In, func(t *testing.T) {
			require.Equal(t, c.Out, events.NormalizeString(c.In), c.Why)
		})
	}
}
