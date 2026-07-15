//go:build windows

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWindowsSpotifyIntegration(t *testing.T) {
	if os.Getenv("SEIZEN_TEST_SPOTIFY") != "1" {
		t.Skip("set SEIZEN_TEST_SPOTIFY=1 for the local Windows integration check")
	}
	controller := newPlatformMediaController()
	defer controller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	state, err := controller.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Available {
		t.Skip("Spotify is not exposing an active Windows media session")
	}
	if state.Title == "" {
		t.Fatal("Spotify session did not include a track title")
	}
	if !strings.HasPrefix(state.ArtworkDataURL, "data:image/") {
		t.Fatal("Spotify session did not include safe album artwork")
	}
	t.Logf("Spotify: %s — %s (%s), artwork=%d bytes, %d/%d seconds",
		state.Title, state.Artist, state.PlaybackStatus, len(state.ArtworkDataURL),
		state.PositionSeconds, state.DurationSeconds)
}

func TestWindowsSpotifyToggleIntegration(t *testing.T) {
	if os.Getenv("SEIZEN_TEST_SPOTIFY_CONTROL") != "1" {
		t.Skip("set SEIZEN_TEST_SPOTIFY_CONTROL=1 for the reversible control check")
	}
	controller := newPlatformMediaController()
	defer controller.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := controller.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Available || !before.CanToggle {
		t.Skip("Spotify does not expose play/pause right now")
	}
	after, err := controller.Control(ctx, "toggle")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer restoreCancel()
		current, stateErr := controller.State(restoreCtx)
		if stateErr == nil && current.PlaybackStatus != before.PlaybackStatus {
			_, _ = controller.Control(restoreCtx, "toggle")
		}
	}()
	if after.PlaybackStatus == before.PlaybackStatus {
		t.Fatalf("toggle left Spotify in %q", after.PlaybackStatus)
	}
	restored, err := controller.Control(ctx, "toggle")
	if err != nil {
		t.Fatal(err)
	}
	if restored.PlaybackStatus != before.PlaybackStatus {
		t.Fatalf("Spotify was not restored to %q; got %q", before.PlaybackStatus, restored.PlaybackStatus)
	}
}
