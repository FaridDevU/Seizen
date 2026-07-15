package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const mediaRequestTimeout = 5 * time.Second
const maximumMediaArtworkSize = 1 << 20

var errMediaControllerClosed = errors.New("the player is already closed")

// MediaPlaybackState is the Spotify session that Windows currently exposes
// through its system media controls. It intentionally contains no account or
// credential data.
type MediaPlaybackState struct {
	Available       bool   `json:"available"`
	Source          string `json:"source"`
	Title           string `json:"title"`
	Artist          string `json:"artist"`
	Album           string `json:"album"`
	ArtworkDataURL  string `json:"artworkDataURL"`
	PlaybackStatus  string `json:"playbackStatus"`
	PositionSeconds int64  `json:"positionSeconds"`
	DurationSeconds int64  `json:"durationSeconds"`
	CanToggle       bool   `json:"canToggle"`
	CanNext         bool   `json:"canNext"`
	CanPrevious     bool   `json:"canPrevious"`
	ErrorMessage    string `json:"errorMessage"`
	TrackKey        string `json:"trackKey"`
}

type mediaController interface {
	State(context.Context) (MediaPlaybackState, error)
	Control(context.Context, string) (MediaPlaybackState, error)
	Close()
}

// GetSpotifyPlayback reads only Spotify's Windows media session. It never
// falls back to controlling another application that happens to be playing.
func (a *App) GetSpotifyPlayback() (MediaPlaybackState, error) {
	ctx, cancel := context.WithTimeout(a.context(), mediaRequestTimeout)
	defer cancel()
	state, err := a.spotifyMediaController().State(ctx)
	state.TrackKey = mediaTrackKey(state)
	return state, err
}

// GetSpotifyPlaybackSince skips the artwork (tens or hundreds of KB over the
// bridge) when the track has not changed relative to knownTrackKey; the
// frontend keeps the one it already has.
func (a *App) GetSpotifyPlaybackSince(knownTrackKey string) (MediaPlaybackState, error) {
	state, err := a.GetSpotifyPlayback()
	if err == nil && knownTrackKey != "" && knownTrackKey == state.TrackKey {
		state.ArtworkDataURL = ""
	}
	return state, err
}

func mediaTrackKey(state MediaPlaybackState) string {
	if !state.Available {
		return ""
	}
	return state.Title + "\x00" + state.Artist + "\x00" + state.Album
}

// ControlSpotifyPlayback applies an allow-listed media command and returns the
// verified state reported by Windows afterwards.
func (a *App) ControlSpotifyPlayback(action string) (MediaPlaybackState, error) {
	action, err := validateMediaAction(action)
	if err != nil {
		return MediaPlaybackState{}, err
	}
	ctx, cancel := context.WithTimeout(a.context(), mediaRequestTimeout)
	defer cancel()
	state, err := a.spotifyMediaController().Control(ctx, action)
	state.TrackKey = mediaTrackKey(state)
	return state, err
}

func (a *App) spotifyMediaController() mediaController {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.media == nil {
		a.media = newPlatformMediaController()
	}
	return a.media
}

func validateMediaAction(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "previous", "toggle", "next":
		return action, nil
	default:
		return "", fmt.Errorf("media action %q is not allowed", action)
	}
}

func isSpotifyMediaSource(source string) bool {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "spotify.exe" {
		return true
	}
	return strings.HasPrefix(source, "spotifyab.spotifymusic_") &&
		(strings.HasSuffix(source, "!spotify") || strings.HasSuffix(source, "!app"))
}

func spotifyStateChanged(action string, before, after MediaPlaybackState) bool {
	switch action {
	case "toggle":
		return after.PlaybackStatus != before.PlaybackStatus &&
			(after.PlaybackStatus == "playing" || after.PlaybackStatus == "paused")
	case "next", "previous":
		return after.Title != "" &&
			(after.Title != before.Title || after.Artist != before.Artist || after.Album != before.Album)
	default:
		return true
	}
}

func mediaArtworkDataURL(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	if len(data) > maximumMediaArtworkSize {
		return "", errors.New("the Spotify artwork exceeds the safe limit")
	}
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", fmt.Errorf("Spotify returned artwork that is not allowed: %s", mimeType)
	}
}
