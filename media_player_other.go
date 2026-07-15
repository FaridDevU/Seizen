//go:build !windows

package main

import (
	"context"
	"errors"
)

type unsupportedMediaController struct{}

func newPlatformMediaController() mediaController { return unsupportedMediaController{} }

func (unsupportedMediaController) State(context.Context) (MediaPlaybackState, error) {
	return MediaPlaybackState{
		PlaybackStatus: "unavailable",
		ErrorMessage:   "The Spotify player requires Windows 10 1809 or later.",
	}, nil
}

func (unsupportedMediaController) Control(context.Context, string) (MediaPlaybackState, error) {
	return MediaPlaybackState{}, errors.New("Spotify media controls are not available")
}

func (unsupportedMediaController) Close() {}
