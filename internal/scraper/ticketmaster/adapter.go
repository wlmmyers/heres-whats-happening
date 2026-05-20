// Package ticketmaster implements a scraper Adapter against the Ticketmaster
// Discovery API v2.
package ticketmaster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

const defaultBaseURL = "https://app.ticketmaster.com"

// Adapter fetches events from the Discovery API for a single city.
type Adapter struct {
	baseURL string
	apiKey  string
	city    string
	http    *http.Client
}

// New builds an Adapter. baseURL is overridable for tests (use httptest.Server.URL).
// In production, pass "" to get the default Ticketmaster URL.
func New(baseURL, apiKey, city string) *Adapter {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Adapter{
		baseURL: baseURL,
		apiKey:  apiKey,
		city:    city,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *Adapter) Name() string { return "ticketmaster" }

func (a *Adapter) Fetch(ctx context.Context) ([]events.Message, error) {
	q := url.Values{}
	q.Set("apikey", a.apiKey)
	q.Set("city", a.city)
	q.Set("size", "200")

	endpoint := a.baseURL + "/discovery/v2/events.json?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ticketmaster %d: %s", resp.StatusCode, string(body))
	}
	var payload discoveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	out := make([]events.Message, 0, len(payload.Embedded.Events))
	for _, e := range payload.Embedded.Events {
		msg, ok := e.toMessage()
		if !ok {
			continue
		}
		out = append(out, msg)
	}
	return out, nil
}

// ---- Discovery API DTO ----------------------------------------------------

type discoveryResponse struct {
	Embedded struct {
		Events []discoveryEvent `json:"events"`
	} `json:"_embedded"`
}

type discoveryEvent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Info   string `json:"info"`
	Images []struct {
		URL string `json:"url"`
	} `json:"images"`
	Dates struct {
		Start struct {
			DateTime string `json:"dateTime"`
		} `json:"start"`
		End struct {
			DateTime string `json:"dateTime"`
		} `json:"end"`
	} `json:"dates"`
	Classifications []struct {
		Genre    struct{ Name string `json:"name"` } `json:"genre"`
		SubGenre struct{ Name string `json:"name"` } `json:"subGenre"`
	} `json:"classifications"`
	Embedded struct {
		Venues []struct {
			Name    string `json:"name"`
			Address struct {
				Line1 string `json:"line1"`
			} `json:"address"`
			Location struct {
				Latitude  string `json:"latitude"`
				Longitude string `json:"longitude"`
			} `json:"location"`
			URL string `json:"url"`
		} `json:"venues"`
		Attractions []struct {
			Name string `json:"name"`
		} `json:"attractions"`
	} `json:"_embedded"`
}

func (e *discoveryEvent) toMessage() (events.Message, bool) {
	if e.ID == "" || e.Name == "" {
		return events.Message{}, false
	}
	startsAt, err := time.Parse(time.RFC3339, e.Dates.Start.DateTime)
	if err != nil {
		return events.Message{}, false
	}
	if len(e.Embedded.Venues) == 0 {
		return events.Message{}, false
	}
	v := e.Embedded.Venues[0]

	venue := events.Venue{
		Name:       v.Name,
		Address:    v.Address.Line1,
		WebsiteURL: v.URL,
	}
	if v.Location.Latitude != "" {
		if lat, err := strconv.ParseFloat(v.Location.Latitude, 64); err == nil {
			venue.Lat = &lat
		}
	}
	if v.Location.Longitude != "" {
		if lng, err := strconv.ParseFloat(v.Location.Longitude, 64); err == nil {
			venue.Lng = &lng
		}
	}

	performers := make([]string, 0, len(e.Embedded.Attractions))
	for _, a := range e.Embedded.Attractions {
		if a.Name != "" {
			performers = append(performers, a.Name)
		}
	}

	genreSet := map[string]struct{}{}
	for _, c := range e.Classifications {
		if g := events.NormalizeGenre(c.Genre.Name); g != "" {
			genreSet[g] = struct{}{}
		}
		if g := events.NormalizeGenre(c.SubGenre.Name); g != "" {
			genreSet[g] = struct{}{}
		}
	}
	genres := make([]string, 0, len(genreSet))
	for g := range genreSet {
		genres = append(genres, g)
	}

	msg := events.Message{
		SourceID:      "ticketmaster",
		SourceEventID: e.ID,
		Title:         e.Name,
		Description:   e.Info,
		StartsAt:      startsAt,
		Venue:         venue,
		Performers:    performers,
		Genres:        genres,
		URL:           e.URL,
	}
	if e.Dates.End.DateTime != "" {
		if endsAt, err := time.Parse(time.RFC3339, e.Dates.End.DateTime); err == nil {
			msg.EndsAt = &endsAt
		}
	}
	if len(e.Images) > 0 {
		msg.ImageURL = e.Images[0].URL
	}
	return msg, true
}
