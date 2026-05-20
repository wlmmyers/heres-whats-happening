package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wmyers/heres-whats-happening/internal/events"
)

type fakeAdapter struct {
	msgs []events.Message
	err  error
}

func (f *fakeAdapter) Name() string { return "fake" }
func (f *fakeAdapter) Fetch(ctx context.Context) ([]events.Message, error) {
	return f.msgs, f.err
}

type fakePublisher struct {
	sent [][]byte
}

func (p *fakePublisher) Send(ctx context.Context, queueURL string, body []byte) error {
	p.sent = append(p.sent, body)
	return nil
}

func TestRunner_Run_PublishesEachEvent(t *testing.T) {
	a := &fakeAdapter{msgs: []events.Message{
		{SourceID: "fake", SourceEventID: "1", Title: "A", StartsAt: time.Now(), Venue: events.Venue{Name: "v"}},
		{SourceID: "fake", SourceEventID: "2", Title: "B", StartsAt: time.Now(), Venue: events.Venue{Name: "v"}},
	}}
	p := &fakePublisher{}
	r := NewRunner(a, p, "http://localhost/queue")
	require.NoError(t, r.Run(context.Background()))
	require.Len(t, p.sent, 2)

	var m1 events.Message
	require.NoError(t, json.Unmarshal(p.sent[0], &m1))
	require.Equal(t, "A", m1.Title)
}

func TestRunner_Run_AdapterError_Propagates(t *testing.T) {
	a := &fakeAdapter{err: errors.New("boom")}
	p := &fakePublisher{}
	r := NewRunner(a, p, "http://localhost/queue")
	require.Error(t, r.Run(context.Background()))
	require.Empty(t, p.sent)
}
