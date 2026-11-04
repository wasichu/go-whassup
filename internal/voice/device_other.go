//go:build !darwin && !linux

package voice

import (
	"context"
	"errors"
	"io"
)

type nativeDevice struct{}

func (nativeDevice) capture(context.Context, func([]int16) error) error {
	return errors.New("voice recording requires macOS or Linux")
}
func (nativeDevice) play(context.Context, io.Reader, int) error {
	return errors.New("voice playback requires macOS or Linux")
}
