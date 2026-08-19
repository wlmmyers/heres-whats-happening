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
	"regexp"
	"strconv"
	"time"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

const defaultBaseURL = "https://app.ticketmaster.com"

const (
	// pageSize is the Discovery API's maximum page size.
	pageSize = 200
	// pagingDepth is Ticketmaster's deep-paging cap: a request whose
	// page*size exceeds it is rejected with HTTP 400 / DIS1035.
	pagingDepth = 1000
	// maxPages bounds pagination. The cap is inclusive -- at size=200,
	// page=5 (page*size == 1000) is served and page=6 is rejected -- so the
	// last reachable page index is pagingDepth/pageSize and the count of
	// reachable pages is one more than that. Past it the API advertises
	// "next" links it will not honour, so there is nothing to gain by
	// following them.
	maxPages = pagingDepth/pageSize + 1
	// sortOrder must stay identical across every page of one walk. Paging is
	// an offset into a sorted result set, so a page fetched under a different
	// order silently skips events rather than continuing where the last left
	// off.
	sortOrder = "date,asc"
)

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

// Fetch walks every page the API offers, following _links.next.href until it
// runs out or maxPages is reached.
func (a *Adapter) Fetch(ctx context.Context) ([]events.Message, error) {
	q := url.Values{}
	q.Set("apikey", a.apiKey)
	q.Set("city", a.city)
	q.Set("size", strconv.Itoa(pageSize))
	q.Set("sort", sortOrder)
	q.Set("startDateTime", time.Now().UTC().Format(time.RFC3339)) // today, probably not needed but just in case
	endpoint := a.baseURL + "/discovery/v2/events.json?" + q.Encode()

	out := make([]events.Message, 0, pageSize)
	// Paging happens over a live result set, so an event can slide from one
	// page onto the next between requests and be returned twice.
	seen := make(map[string]struct{}, pageSize)

	for page := 0; page < maxPages && endpoint != ""; page++ {
		payload, err := a.fetchPage(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		for _, e := range payload.Embedded.Events {
			msg, ok := e.toMessage()
			if !ok {
				continue
			}
			if _, dup := seen[msg.SourceEventID]; dup {
				continue
			}
			seen[msg.SourceEventID] = struct{}{}
			out = append(out, msg)
		}
		endpoint = a.nextPageURL(payload.Links.Next.Href)
	}
	return out, nil
}

func (a *Adapter) fetchPage(ctx context.Context, endpoint string) (*discoveryResponse, error) {
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
	return &payload, nil
}

// uriTemplateVars matches the RFC 6570 expansions Ticketmaster leaves in its
// paging links, e.g. "/discovery/v2/events.json?page=1{&sort}".
var uriTemplateVars = regexp.MustCompile(`\{[^}]*\}`)

// nextPageURL turns a _links.next.href into an absolute URL to fetch. The href
// is relative to the API host, carries unexpanded URI template vars, and drops
// the apikey, so all three need fixing up before it is usable. An empty string
// means there is no further page.
//
// The href supplies the page cursor and carries the other query params
// forward, but apikey and sort are re-pinned to the values the walk started
// with. Live responses do expand sort into the link, which makes that a no-op,
// but a "{&sort}" template does not -- and stripping the template would
// otherwise hand the next page to the API's default ordering.
func (a *Adapter) nextPageURL(href string) string {
	if href == "" {
		return ""
	}
	ref, err := url.Parse(uriTemplateVars.ReplaceAllString(href, ""))
	if err != nil {
		return ""
	}
	base, err := url.Parse(a.baseURL)
	if err != nil {
		return ""
	}
	next := base.ResolveReference(ref)
	q := next.Query()
	q.Set("apikey", a.apiKey)
	q.Set("sort", sortOrder)
	next.RawQuery = q.Encode()
	return next.String()
}

// ---- Discovery API DTO ----------------------------------------------------

type discoveryResponse struct {
	Embedded struct {
		Events []discoveryEvent `json:"events"`
	} `json:"_embedded"`
	Links struct {
		Next struct {
			Href string `json:"href"`
		} `json:"next"`
	} `json:"_links"`
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
			DateTime  string `json:"dateTime"`
			LocalDate string `json:"localDate"`
		} `json:"start"`
		End struct {
			DateTime  string `json:"dateTime"`
			LocalDate string `json:"localDate"`
		} `json:"end"`
		// Timezone is the venue's IANA zone, e.g. "America/Los_Angeles". It
		// is what a bare localDate is expressed in.
		Timezone string `json:"timezone"`
	} `json:"dates"`
	Classifications []struct {
		// Primary marks the classification that describes the event itself.
		// Events with several attractions carry one entry each; live Seattle
		// data had 31 of 200 in that shape, every one of them flagging exactly
		// one primary.
		Primary bool `json:"primary"`
		Segment struct {
			Name string `json:"name"`
		} `json:"segment"`
		Genre struct {
			Name string `json:"name"`
		} `json:"genre"`
		SubGenre struct {
			Name string `json:"name"`
		} `json:"subGenre"`
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
			Timezone string `json:"timezone"`
			URL      string `json:"url"`
		} `json:"venues"`
		Attractions []struct {
			Name string `json:"name"`
		} `json:"attractions"`
	} `json:"_embedded"`
}

// location resolves the venue's timezone, which is what a bare localDate is
// expressed in. A localDate read as UTC would put any event west of Greenwich
// on the previous calendar day, so this matters for date-based filtering.
//
// Most events carry it on dates.timezone; a minority omit it there but still
// name it on the venue, so both are tried.
//
// Falls back to UTC when neither names a zone or the host has no tzdata for
// it: a shifted timestamp is still better than dropping the event, and the
// dropping is what this whole path exists to avoid.
func (e *discoveryEvent) location() *time.Location {
	candidates := []string{e.Dates.Timezone}
	if len(e.Embedded.Venues) > 0 {
		candidates = append(candidates, e.Embedded.Venues[0].Timezone)
	}
	for _, name := range candidates {
		if name == "" {
			continue
		}
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	return time.UTC
}

// endsAt resolves the event's end, preferring an exact dateTime and falling
// back to a bare localDate the way the start does.
//
// An end localDate is the last day the event runs, not an instant, so it
// resolves to the final second of that day in the venue's zone -- taking it as
// midnight would drop the whole closing day from the event's span. Reports
// false when the API gives no usable end, which is the common case.
func (e *discoveryEvent) endsAt() (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, e.Dates.End.DateTime); err == nil {
		return t, true
	}
	day, err := time.ParseInLocation("2006-01-02", e.Dates.End.LocalDate, e.location())
	if err != nil {
		return time.Time{}, false
	}
	y, m, d := day.Date()
	return time.Date(y, m, d, 23, 59, 59, 0, day.Location()), true
}

// segment resolves the event's top-level category from the primary
// classification, falling back to the first when nothing is flagged primary.
//
// Only the segment is decided this way. Genres deliberately union across every
// classification, because a multi-attraction bill's supporting tags are still
// tags the matcher scores on -- but its supporting SEGMENT is not what the
// event is, and letting the last entry win would file a rock show under
// whatever its opener was classified as.
//
// Returns "" when the event carries no classification at all, which the ingest
// stores as SQL NULL.
func (e *discoveryEvent) segment() string {
	if len(e.Classifications) == 0 {
		return ""
	}
	c := e.Classifications[0]
	for _, cand := range e.Classifications {
		if cand.Primary {
			c = cand
			break
		}
	}
	return events.NormalizeSegment(c.Segment.Name)
}

func (e *discoveryEvent) toMessage() (events.Message, bool) {
	timeTBD := false
	if e.ID == "" || e.Name == "" {
		return events.Message{}, false
	}
	startsAt, err := time.Parse(time.RFC3339, e.Dates.Start.DateTime)
	if err != nil {
		// Check for a localDate (YYYY-MM-DD) with no time. The Discovery API returns this for events whose start time is TBD.
		localDate, lerr := time.ParseInLocation("2006-01-02", e.Dates.Start.LocalDate, e.location())
		if lerr != nil {
			return events.Message{}, false
		}
		startsAt = localDate
		timeTBD = true
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
		Segment:       e.segment(),
		Performers:    performers,
		Genres:        genres,
		URL:           e.URL,
		TimeTBD:       timeTBD,
	}
	if endsAt, ok := e.endsAt(); ok {
		msg.EndsAt = &endsAt
	}
	if len(e.Images) > 0 {
		msg.ImageURL = e.Images[0].URL
	}
	return msg, true
}
