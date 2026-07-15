package main

import "testing"

func TestValidateMediaAction(t *testing.T) {
	for _, action := range []string{"previous", "toggle", "next", " NEXT "} {
		if _, err := validateMediaAction(action); err != nil {
			t.Fatalf("validateMediaAction(%q): %v", action, err)
		}
	}
	if _, err := validateMediaAction("launch"); err == nil {
		t.Fatal("validateMediaAction accepted an unsafe action")
	}
}

func TestSpotifySourceFilter(t *testing.T) {
	if !isSpotifyMediaSource("Spotify.exe") ||
		!isSpotifyMediaSource("SpotifyAB.SpotifyMusic_zpdnekdrzrea0!Spotify") {
		t.Fatal("known Spotify sources were rejected")
	}
	if isSpotifyMediaSource("Microsoft.ZuneMusic") || isSpotifyMediaSource("my-spotify-player.exe") {
		t.Fatal("a non-Spotify source was accepted")
	}
}

func TestSpotifyStateChangeRequiresVerifiedResult(t *testing.T) {
	before := MediaPlaybackState{Available: true, Title: "One", Artist: "Artist", PlaybackStatus: "paused"}
	if spotifyStateChanged("toggle", before, before) {
		t.Fatal("an accepted command was mistaken for a playback change")
	}
	playing := before
	playing.PlaybackStatus = "playing"
	if !spotifyStateChanged("toggle", before, playing) {
		t.Fatal("a verified playback change was missed")
	}
	next := playing
	next.Title = "Two"
	if !spotifyStateChanged("next", playing, next) {
		t.Fatal("a verified track change was missed")
	}
}

func TestMediaArtworkAcceptsOnlySmallRasterImages(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}
	dataURL, err := mediaArtworkDataURL(jpeg)
	if err != nil || len(dataURL) < len("data:image/jpeg;base64,") || dataURL[:len("data:image/jpeg;base64,")] != "data:image/jpeg;base64," {
		t.Fatalf("safe JPEG was rejected: %q, %v", dataURL, err)
	}
	if _, err = mediaArtworkDataURL([]byte("<svg><script/></svg>")); err == nil {
		t.Fatal("active image content was accepted")
	}
	if _, err = mediaArtworkDataURL(make([]byte, maximumMediaArtworkSize+1)); err == nil {
		t.Fatal("oversized artwork was accepted")
	}
}
