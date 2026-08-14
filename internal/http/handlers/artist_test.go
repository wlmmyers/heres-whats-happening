package handlers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/store"
)

func TestBuildArtist_MapsEveryPopulatedSection(t *testing.T) {
	ok := "ok"
	url := "https://upload.wikimedia.org/pb.jpg"
	w, hgt := int32(640), int32(427)
	bio := "Phoebe Bridgers is an American singer-songwriter."
	tour := "Reunion Tour"
	blurb := "Currently out on the Reunion Tour."

	row := store.GetArtistEnrichmentBatchRow{
		DisplayName:  "Phoebe Bridgers",
		ArtistStatus: "ok",
		ImageStatus:  &ok,
		ImageUrl:     &url,
		ImageWidth:   &w,
		ImageHeight:  &hgt,
		ImageCredit:  []byte(`{"license_short_name":"CC BY-SA 4.0","attribution_required":true}`),
		BioStatus:    &ok,
		BioMd:        &bio,
		BioSources:   []byte(`[{"kind":"wikipedia","title":"Phoebe Bridgers"}]`),
		TourStatus:   &ok,
		TourName:     &tour,
		Songs:        []byte(`[{"name":"Motion Sickness"}]`),
		Blurb:        &blurb,
	}

	a := buildArtist(row)
	require.Equal(t, "Phoebe Bridgers", a.Name)
	require.NotNil(t, a.Image)
	require.Equal(t, url, a.Image.URL)
	require.Equal(t, 640, a.Image.Width)
	require.NotNil(t, a.Bio)
	require.Contains(t, a.Bio.Text, "singer-songwriter")
	require.NotNil(t, a.Tour)
	require.Equal(t, "Reunion Tour", a.Tour.Name)
	// a.Tour.Songs is a json.RawMessage passthrough (raw bytes), so require.Len
	// on it directly would count bytes, not array elements. Decode first to
	// check there's exactly one song, which is what this test intends.
	var songs []json.RawMessage
	require.NoError(t, json.Unmarshal(a.Tour.Songs, &songs))
	require.Len(t, songs, 1)
}

// A resolved artist with no successful enrichment must produce an object with
// no image/bio/tour keys at all, not empty ones — the FE distinguishes absent
// from empty.
func TestBuildArtist_OmitsFailedSections(t *testing.T) {
	none := "none"
	row := store.GetArtistEnrichmentBatchRow{
		DisplayName:  "Some Local Opener",
		ArtistStatus: "not_found",
		ImageStatus:  &none,
		BioStatus:    &none,
	}

	a := buildArtist(row)
	require.Nil(t, a.Image)
	require.Nil(t, a.Bio)
	require.Nil(t, a.Tour)

	out, err := json.Marshal(a)
	require.NoError(t, err)
	require.NotContains(t, string(out), `"image"`)
	require.NotContains(t, string(out), `"bio"`)
	require.NotContains(t, string(out), `"tour"`)
}
