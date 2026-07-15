//go:build windows

package core

import (
	"context"
	"errors"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go/windows/media/control"
	"golang.org/x/sys/windows"
)

const (
	iidIStream           = "0000000c-0000-0000-C000-000000000046"
	mediaArtworkReadSize = 64 << 10
)

var createStreamOverRandomAccessStream = windows.NewLazySystemDLL("shcore.dll").NewProc("CreateStreamOverRandomAccessStream")

type mediaIStream struct {
	ole.IUnknown
}

type mediaIStreamVTable struct {
	ole.IUnknownVtbl
	Read uintptr
}

func (stream *mediaIStream) read(buffer []byte) (uint32, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	var read uint32
	hresult, _, _ := syscall.SyscallN(
		(*mediaIStreamVTable)(unsafe.Pointer(stream.RawVTable)).Read,
		uintptr(unsafe.Pointer(stream)),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&read)),
	)
	if int32(hresult) < 0 {
		return 0, ole.NewError(hresult)
	}
	return read, nil
}

// Windows supplies Spotify's artwork as an IRandomAccessStream. ShCore already
// provides the small, native bridge to a regular COM IStream, so no image SDK
// or Spotify credentials are needed here.
func spotifyArtworkDataURL(
	ctx context.Context,
	properties *control.GlobalSystemMediaTransportControlsSessionMediaProperties,
) (string, error) {
	reference, err := properties.GetThumbnail()
	if err != nil || reference == nil {
		return "", err
	}
	defer reference.Release()
	operation, err := reference.OpenReadAsync()
	if err != nil {
		return "", err
	}
	defer operation.Release()
	rawStream, err := waitWinRTAsync(ctx, operation)
	if err != nil || rawStream == nil {
		return "", err
	}
	randomAccessStream := (*ole.IInspectable)(rawStream)
	defer randomAccessStream.Release()

	var stream *mediaIStream
	hresult, _, _ := createStreamOverRandomAccessStream.Call(
		uintptr(rawStream),
		uintptr(unsafe.Pointer(ole.NewGUID(iidIStream))),
		uintptr(unsafe.Pointer(&stream)),
	)
	if int32(hresult) < 0 {
		return "", ole.NewError(hresult)
	}
	if stream == nil {
		return "", errors.New("Windows did not return the Spotify artwork")
	}
	defer stream.Release()

	data := make([]byte, 0, mediaArtworkReadSize)
	for len(data) <= maximumMediaArtworkSize {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		buffer := make([]byte, mediaArtworkReadSize)
		count, readErr := stream.read(buffer)
		if readErr != nil {
			return "", readErr
		}
		if count > uint32(len(buffer)) {
			return "", errors.New("Windows returned artwork with an invalid size")
		}
		data = append(data, buffer[:count]...)
		if count < uint32(len(buffer)) {
			break
		}
	}
	return mediaArtworkDataURL(data)
}
