//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/media/control"
	"golang.org/x/sys/windows"
)

const roInitMultithreaded = 1
const mediaStateVerificationTimeout = 1500 * time.Millisecond
const mediaStatePollDelay = 75 * time.Millisecond

const (
	mediaSessionManagerClass       = "Windows.Media.Control.GlobalSystemMediaTransportControlsSessionManager"
	mediaSessionManagerStaticsGUID = "2050c4ee-11a0-57de-aed7-c97c70338245"
)

var roUninitialize = windows.NewLazySystemDLL("combase.dll").NewProc("RoUninitialize")
var roInitialize = windows.NewLazySystemDLL("combase.dll").NewProc("RoInitialize")

type mediaSessionManagerStatics struct {
	ole.IInspectable
}

type mediaSessionManagerStaticsVTable struct {
	ole.IInspectableVtbl
	RequestAsync uintptr
}

func (v *mediaSessionManagerStatics) vTable() *mediaSessionManagerStaticsVTable {
	return (*mediaSessionManagerStaticsVTable)(unsafe.Pointer(v.RawVTable))
}

type mediaWorkerRequest struct {
	ctx    context.Context
	action string
	result chan mediaWorkerResult
}

type mediaWorkerResult struct {
	state MediaPlaybackState
	err   error
}

type mediaArtworkCache struct {
	trackKey string
	dataURL  string
}

type windowsMediaController struct {
	requests  chan mediaWorkerRequest
	closed    chan struct{}
	done      chan struct{}
	lifetime  context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

func newPlatformMediaController() mediaController {
	lifetime, cancel := context.WithCancel(context.Background())
	controller := &windowsMediaController{
		requests: make(chan mediaWorkerRequest),
		closed:   make(chan struct{}),
		done:     make(chan struct{}),
		lifetime: lifetime,
		cancel:   cancel,
	}
	go controller.run()
	return controller
}

func (c *windowsMediaController) State(ctx context.Context) (MediaPlaybackState, error) {
	return c.request(ctx, "")
}

func (c *windowsMediaController) Control(ctx context.Context, action string) (MediaPlaybackState, error) {
	return c.request(ctx, action)
}

func (c *windowsMediaController) Close() {
	c.closeOnce.Do(func() {
		c.cancel()
		close(c.closed)
		<-c.done
	})
}

func (c *windowsMediaController) request(ctx context.Context, action string) (MediaPlaybackState, error) {
	requestContext, cancel := context.WithCancel(ctx)
	stopCancellation := context.AfterFunc(c.lifetime, cancel)
	defer stopCancellation()
	defer cancel()
	request := mediaWorkerRequest{
		ctx:    requestContext,
		action: action,
		result: make(chan mediaWorkerResult, 1),
	}
	select {
	case c.requests <- request:
	case <-c.closed:
		return MediaPlaybackState{}, errMediaControllerClosed
	case <-requestContext.Done():
		return MediaPlaybackState{}, requestContext.Err()
	}
	select {
	case result := <-request.result:
		return result.state, result.err
	case <-c.closed:
		return MediaPlaybackState{}, errMediaControllerClosed
	case <-requestContext.Done():
		return MediaPlaybackState{}, requestContext.Err()
	}
}

// WinRT objects belong to the apartment where they were created. A single
// locked worker keeps every Spotify query and command on that same thread.
func (c *windowsMediaController) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(c.done)

	initialized, initErr := initializeWinRT()
	if initialized {
		defer roUninitialize.Call()
	}

	var manager *control.GlobalSystemMediaTransportControlsSessionManager
	if initErr == nil {
		ctx, cancel := context.WithTimeout(c.lifetime, mediaRequestTimeout)
		manager, initErr = requestMediaSessionManager(ctx)
		cancel()
	}
	if manager != nil {
		defer manager.Release()
	}
	artwork := mediaArtworkCache{}

	for {
		select {
		case <-c.closed:
			return
		case request := <-c.requests:
			result := mediaWorkerResult{err: initErr}
			if initErr == nil {
				result.state, result.err = safelyHandleMediaRequest(request.ctx, manager, request.action, &artwork)
			}
			request.result <- result
		}
	}
}

func initializeWinRT() (bool, error) {
	hresult, _, _ := roInitialize.Call(roInitMultithreaded)
	if int32(hresult) < 0 {
		return false, fmt.Errorf("Windows could not start the media controls: %w", ole.NewError(hresult))
	}
	// S_OK and S_FALSE both require a matching RoUninitialize call.
	return true, nil
}

func requestMediaSessionManager(ctx context.Context) (*control.GlobalSystemMediaTransportControlsSessionManager, error) {
	operation, err := requestMediaSessionManagerAsync()
	if err != nil {
		return nil, fmt.Errorf("Windows did not allow querying the media sessions: %w", err)
	}
	defer operation.Release()
	result, err := waitWinRTAsync(ctx, operation)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Windows did not return the media manager")
	}
	return (*control.GlobalSystemMediaTransportControlsSessionManager)(result), nil
}

// The generated helper does not release its activation factory. Keeping this
// tiny ABI call local makes ownership explicit while still using the generated
// types for every public WinRT object.
func requestMediaSessionManagerAsync() (*foundation.IAsyncOperation, error) {
	factory, err := ole.RoGetActivationFactory(
		mediaSessionManagerClass,
		ole.NewGUID(mediaSessionManagerStaticsGUID),
	)
	if err != nil {
		return nil, err
	}
	defer factory.Release()
	statics := (*mediaSessionManagerStatics)(unsafe.Pointer(factory))
	var operation *foundation.IAsyncOperation
	hresult, _, _ := syscall.SyscallN(
		statics.vTable().RequestAsync,
		uintptr(unsafe.Pointer(statics)),
		uintptr(unsafe.Pointer(&operation)),
	)
	if int32(hresult) < 0 {
		return nil, ole.NewError(hresult)
	}
	return operation, nil
}

func safelyHandleMediaRequest(
	ctx context.Context,
	manager *control.GlobalSystemMediaTransportControlsSessionManager,
	action string,
	artwork *mediaArtworkCache,
) (state MediaPlaybackState, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			state = MediaPlaybackState{}
			err = fmt.Errorf("Windows rejected the media session: %v", recovered)
		}
	}()

	session, err := findSpotifySession(manager)
	if err != nil {
		return MediaPlaybackState{}, err
	}
	if session == nil {
		return MediaPlaybackState{
			Source:         "Spotify",
			PlaybackStatus: "unavailable",
		}, nil
	}
	defer session.Release()

	if action != "" {
		before, _ := readSpotifyState(ctx, session, artwork)
		if err = invokeSpotifyControl(ctx, session, action); err != nil {
			return MediaPlaybackState{}, err
		}
		if before.Available {
			return waitForSpotifyStateChange(ctx, session, action, before, artwork)
		}
	}
	return readSpotifyState(ctx, session, artwork)
}

func waitForSpotifyStateChange(
	ctx context.Context,
	session *control.GlobalSystemMediaTransportControlsSession,
	action string,
	before MediaPlaybackState,
	artwork *mediaArtworkCache,
) (MediaPlaybackState, error) {
	deadline := time.NewTimer(mediaStateVerificationTimeout)
	defer deadline.Stop()
	latest := before
	var lastErr error
	for {
		state, err := readSpotifyState(ctx, session, artwork)
		if err == nil {
			latest = state
			if spotifyStateChanged(action, before, state) {
				return state, nil
			}
		} else {
			lastErr = err
		}

		timer := time.NewTimer(mediaStatePollDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return MediaPlaybackState{}, ctx.Err()
		case <-deadline.C:
			timer.Stop()
			if latest.Available {
				return latest, nil
			}
			return MediaPlaybackState{}, lastErr
		case <-timer.C:
		}
	}
}

func findSpotifySession(
	manager *control.GlobalSystemMediaTransportControlsSessionManager,
) (*control.GlobalSystemMediaTransportControlsSession, error) {
	if manager == nil {
		return nil, errors.New("the media manager is not available")
	}
	sessions, err := manager.GetSessions()
	if err != nil {
		return nil, fmt.Errorf("could not query the media sessions: %w", err)
	}
	if sessions == nil {
		return nil, nil
	}
	defer sessions.Release()
	size, err := sessions.GetSize()
	if err != nil {
		return nil, fmt.Errorf("could not read the media list: %w", err)
	}

	var fallback *control.GlobalSystemMediaTransportControlsSession
	for index := uint32(0); index < size; index++ {
		raw, itemErr := sessions.GetAt(index)
		if itemErr != nil || raw == nil {
			continue
		}
		session := (*control.GlobalSystemMediaTransportControlsSession)(raw)
		source, sourceErr := session.GetSourceAppUserModelId()
		if sourceErr != nil || !isSpotifyMediaSource(source) {
			session.Release()
			continue
		}
		status, statusErr := spotifyPlaybackStatus(session)
		if statusErr == nil && status == control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying {
			if fallback != nil {
				fallback.Release()
			}
			return session, nil
		}
		if fallback == nil {
			fallback = session
		} else {
			session.Release()
		}
	}
	return fallback, nil
}

func readSpotifyState(
	ctx context.Context,
	session *control.GlobalSystemMediaTransportControlsSession,
	artwork *mediaArtworkCache,
) (MediaPlaybackState, error) {
	operation, err := session.TryGetMediaPropertiesAsync()
	if err != nil {
		return MediaPlaybackState{}, fmt.Errorf("Spotify did not return the current song: %w", err)
	}
	defer operation.Release()
	raw, err := waitWinRTAsync(ctx, operation)
	if err != nil {
		return MediaPlaybackState{}, err
	}
	if raw == nil {
		return MediaPlaybackState{}, errors.New("Spotify did not return song information")
	}
	properties := (*control.GlobalSystemMediaTransportControlsSessionMediaProperties)(raw)
	defer properties.Release()

	title, err := properties.GetTitle()
	if err != nil {
		return MediaPlaybackState{}, fmt.Errorf("could not read the Spotify title: %w", err)
	}
	artist, _ := properties.GetArtist()
	album, _ := properties.GetAlbumTitle()
	trackKey := title + "\x00" + artist + "\x00" + album
	if artwork.trackKey != trackKey {
		if dataURL, artworkErr := spotifyArtworkDataURL(ctx, properties); artworkErr == nil {
			artwork.trackKey = trackKey
			artwork.dataURL = dataURL
		}
	}

	playbackInfo, err := session.GetPlaybackInfo()
	if err != nil {
		return MediaPlaybackState{}, fmt.Errorf("could not read the Spotify status: %w", err)
	}
	if playbackInfo == nil {
		return MediaPlaybackState{}, errors.New("Spotify did not return its playback status")
	}
	defer playbackInfo.Release()
	status, err := playbackInfo.GetPlaybackStatus()
	if err != nil {
		return MediaPlaybackState{}, fmt.Errorf("could not read Spotify playback: %w", err)
	}
	controls, err := playbackInfo.GetControls()
	if err != nil {
		return MediaPlaybackState{}, fmt.Errorf("could not read the Spotify controls: %w", err)
	}
	if controls == nil {
		return MediaPlaybackState{}, errors.New("Spotify did not return media controls")
	}
	defer controls.Release()

	canToggle, _ := controls.GetIsPlayPauseToggleEnabled()
	canPlay, _ := controls.GetIsPlayEnabled()
	canPause, _ := controls.GetIsPauseEnabled()
	canNext, _ := controls.GetIsNextEnabled()
	canPrevious, _ := controls.GetIsPreviousEnabled()
	positionSeconds, durationSeconds := spotifyTimeline(session)

	return MediaPlaybackState{
		Available:       true,
		Source:          "Spotify",
		Title:           title,
		Artist:          artist,
		Album:           album,
		ArtworkDataURL:  artwork.dataURL,
		PlaybackStatus:  mediaPlaybackStatusName(status),
		PositionSeconds: positionSeconds,
		DurationSeconds: durationSeconds,
		CanToggle:       canToggle || canPlay || canPause,
		CanNext:         canNext,
		CanPrevious:     canPrevious,
	}, nil
}

func spotifyTimeline(session *control.GlobalSystemMediaTransportControlsSession) (int64, int64) {
	timeline, err := session.GetTimelineProperties()
	if err != nil || timeline == nil {
		return 0, 0
	}
	defer timeline.Release()
	start, startErr := timeline.GetStartTime()
	end, endErr := timeline.GetEndTime()
	position, positionErr := timeline.GetPosition()
	if startErr != nil || endErr != nil || positionErr != nil {
		return 0, 0
	}
	const ticksPerSecond = int64(10_000_000)
	durationSeconds := (end.Duration - start.Duration) / ticksPerSecond
	positionSeconds := (position.Duration - start.Duration) / ticksPerSecond
	if durationSeconds <= 0 {
		return 0, 0
	}
	positionSeconds = max(0, min(positionSeconds, durationSeconds))
	return positionSeconds, durationSeconds
}

func invokeSpotifyControl(
	ctx context.Context,
	session *control.GlobalSystemMediaTransportControlsSession,
	action string,
) error {
	playbackInfo, err := session.GetPlaybackInfo()
	if err != nil {
		return fmt.Errorf("could not check the Spotify control: %w", err)
	}
	if playbackInfo == nil {
		return errors.New("Spotify did not return its playback status")
	}
	defer playbackInfo.Release()
	controls, err := playbackInfo.GetControls()
	if err != nil {
		return fmt.Errorf("could not check the Spotify controls: %w", err)
	}
	if controls == nil {
		return errors.New("Spotify did not return media controls")
	}
	defer controls.Release()

	var operation *foundation.IAsyncOperation
	switch action {
	case "previous":
		enabled, _ := controls.GetIsPreviousEnabled()
		if !enabled {
			return errors.New("Spotify does not allow going back to the previous song right now")
		}
		operation, err = session.TrySkipPreviousAsync()
	case "next":
		enabled, _ := controls.GetIsNextEnabled()
		if !enabled {
			return errors.New("Spotify does not allow skipping to the next song right now")
		}
		operation, err = session.TrySkipNextAsync()
	case "toggle":
		operation, err = toggleSpotifyPlayback(session, playbackInfo, controls)
	default:
		return errors.New("media control not allowed")
	}
	if err != nil {
		return fmt.Errorf("Spotify rejected the control: %w", err)
	}
	if operation == nil {
		return errors.New("Spotify did not start the requested control")
	}
	defer operation.Release()
	accepted, err := waitWinRTAsync(ctx, operation)
	if err != nil {
		return err
	}
	if accepted == nil {
		return errors.New("Spotify rejected the requested control")
	}
	return nil
}

func toggleSpotifyPlayback(
	session *control.GlobalSystemMediaTransportControlsSession,
	playbackInfo *control.GlobalSystemMediaTransportControlsSessionPlaybackInfo,
	controls *control.GlobalSystemMediaTransportControlsSessionPlaybackControls,
) (*foundation.IAsyncOperation, error) {
	if enabled, _ := controls.GetIsPlayPauseToggleEnabled(); enabled {
		return session.TryTogglePlayPauseAsync()
	}
	status, _ := playbackInfo.GetPlaybackStatus()
	if status == control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying {
		if enabled, _ := controls.GetIsPauseEnabled(); enabled {
			return session.TryPauseAsync()
		}
	} else if enabled, _ := controls.GetIsPlayEnabled(); enabled {
		return session.TryPlayAsync()
	}
	return nil, errors.New("the play/pause control is not available")
}

func spotifyPlaybackStatus(
	session *control.GlobalSystemMediaTransportControlsSession,
) (control.GlobalSystemMediaTransportControlsSessionPlaybackStatus, error) {
	info, err := session.GetPlaybackInfo()
	if err != nil {
		return control.GlobalSystemMediaTransportControlsSessionPlaybackStatusClosed, err
	}
	if info == nil {
		return control.GlobalSystemMediaTransportControlsSessionPlaybackStatusClosed, errors.New("Spotify did not return its status")
	}
	defer info.Release()
	return info.GetPlaybackStatus()
}

func mediaPlaybackStatusName(status control.GlobalSystemMediaTransportControlsSessionPlaybackStatus) string {
	switch status {
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying:
		return "playing"
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPaused:
		return "paused"
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusStopped:
		return "stopped"
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusChanging:
		return "changing"
	case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusOpened:
		return "opened"
	default:
		return "closed"
	}
}

func waitWinRTAsync(ctx context.Context, operation *foundation.IAsyncOperation) (unsafe.Pointer, error) {
	if operation == nil {
		return nil, errors.New("Windows did not start the media operation")
	}
	var info *foundation.IAsyncInfo
	if err := operation.PutQueryInterface(ole.NewGUID(foundation.GUIDIAsyncInfo), &info); err != nil {
		return nil, fmt.Errorf("Windows did not return the media status: %w", err)
	}
	defer info.Release()
	defer info.Close()

	// 25ms: enough for WinRT operations without burning CPU on every poll
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := info.GetStatus()
		if err != nil {
			return nil, fmt.Errorf("could not query the media operation: %w", err)
		}
		switch status {
		case foundation.AsyncStatusCompleted:
			return operation.GetResults()
		case foundation.AsyncStatusCanceled:
			return nil, errors.New("Windows canceled the media operation")
		case foundation.AsyncStatusError:
			code, _ := info.GetErrorCode()
			return nil, fmt.Errorf("the media operation failed (0x%08X)", uint32(code.Value))
		}
		select {
		case <-ctx.Done():
			_ = info.Cancel()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
