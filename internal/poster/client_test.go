package poster_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/wmyers/heres-whats-happening/internal/poster"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *poster.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return poster.NewClient(srv.URL, "us-east-1",
		credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""))
}

func TestGenerateSignsTheRequestForLambda(t *testing.T) {
	var gotAuth string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"pngKey":"posters/v1/a/b.png","cached":false}`))
	})

	if _, err := c.Generate(context.Background(), poster.Request{Performer: "La Luz", Venue: "Neumos", Date: "2026-08-20"}); err != nil {
		t.Fatalf("Generate returned %v", err)
	}
	if gotAuth == "" {
		t.Fatal("no Authorization header: the request was not SigV4-signed")
	}
	// Signing for the wrong service silently yields 403s in production.
	if want := "/us-east-1/lambda/aws4_request"; !strings.Contains(gotAuth, want) {
		t.Errorf("Authorization credential scope = %q, want it to contain %q", gotAuth, want)
	}
}

func TestGenerateReturnsKeysOnSuccess(t *testing.T) {
	// The response body still includes a stray "svgKey", as a real Lambda
	// mid-rollout (or one instance behind another) might: the client must
	// tolerate and ignore it rather than fail to decode, and Result must
	// carry no SVG data of any kind — the field does not exist on the type,
	// which this test pins by construction (it would not compile with a
	// res.SvgKey reference if the field ever came back).
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"svgKey":"posters/v1/a/b.svg","pngKey":"posters/v1/a/b.png","cached":true,"artist":{"mbid":"m"}}`))
	})

	res, err := c.Generate(context.Background(), poster.Request{Performer: "x", Venue: "y", Date: "z"})
	if err != nil {
		t.Fatalf("Generate returned %v", err)
	}
	if res.PngKey != "posters/v1/a/b.png" {
		t.Errorf("PngKey = %q", res.PngKey)
	}
	if !res.Cached {
		t.Error("Cached = false, want true")
	}
	if len(res.Artist) == 0 {
		t.Error("Artist was dropped")
	}
}

func TestGenerateMapsA422ToAControlledFailureNotAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"no MusicBrainz match for 'zzz'","stage":"image"}`))
	})

	res, err := c.Generate(context.Background(), poster.Request{Performer: "zzz", Venue: "y", Date: "z"})
	if err != nil {
		t.Fatalf("a controlled 422 must not be an error return, got %v", err)
	}
	if res.FailureStage != "image" {
		t.Errorf("FailureStage = %q, want image", res.FailureStage)
	}
	if res.FailureReason == "" {
		t.Error("FailureReason is empty")
	}
}

func TestGenerateTreatsA5xxAsAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal boom`))
	})

	if _, err := c.Generate(context.Background(), poster.Request{Performer: "x", Venue: "y", Date: "z"}); err == nil {
		t.Fatal("Generate returned nil error on a 500")
	}
}
