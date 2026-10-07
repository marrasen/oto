// Copyright 2021 The Oto Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oto

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/marrasen/oto/v3/internal/mux"
)

var (
	contextCreated       bool
	contextCreationMutex sync.Mutex
)

// ErrCloseUnsupported is returned by Context.Close on the platforms where a
// context cannot be closed yet: Android, browsers, Nintendo SDK and
// PlayStation 5. The context stays usable.
var ErrCloseUnsupported = errors.New("oto: closing a context is not supported on this platform")

// Context is the main object in Oto. It interacts with the audio drivers.
//
// To play sound with Oto, first create a context. Then use the context to create
// an arbitrary number of players. Then use the players to play sound.
//
// Only one context can exist at a time. To create another, for example with
// another sample rate, close the first with Close.
type Context struct {
	context *context

	// closeMu is held by Close, and read-held by the functions that use the
	// driver, so that the driver is not used while it is being closed.
	closeMu sync.RWMutex
	closed  bool
}

// Format is the format of sources.
type Format int

const (
	// FormatFloat32LE is the format of 32 bits floats little endian.
	FormatFloat32LE Format = iota

	// FormatUnsignedInt8 is the format of 8 bits integers.
	FormatUnsignedInt8

	// FormatSignedInt16LE is the format of 16 bits integers little endian.
	FormatSignedInt16LE
)

// NewContextOptions represents options for NewContext.
type NewContextOptions struct {
	// SampleRate specifies the number of samples that should be played during one second.
	// Usual numbers are 44100 or 48000. One context has only one sample rate. You cannot play multiple audio
	// sources with different sample rates at the same time.
	SampleRate int

	// ChannelCount specifies the number of channels. One channel is mono playback. Two
	// channels are stereo playback. No other values are supported.
	ChannelCount int

	// Format specifies the format of sources.
	Format Format

	// BufferSize specifies a buffer size in the underlying device.
	//
	// If 0 is specified, the driver's default buffer size is used.
	// Set BufferSize to adjust the buffer size if you want to adjust latency or reduce noises.
	// Too big buffer size can increase the latency time.
	// On the other hand, too small buffer size can cause glitch noises due to buffer shortage.
	BufferSize time.Duration

	// ApplicationName specifies the name of the client application.
	// It is used for PulseAudio's volume control UI and so on.
	ApplicationName string
}

// NewContext creates a new context with given options.
// A context creates and holds ready-to-use Player objects.
// NewContext returns a context, a channel that closes when initialization finishes, and an error if it exists.
// After the channel closes, call Context.Err to check whether initialization succeeded.
//
// Only one context can exist at a time: NewContext returns an error while
// another context is open. Close the open context first to create another.
func NewContext(options *NewContextOptions) (*Context, chan struct{}, error) {
	contextCreationMutex.Lock()
	defer contextCreationMutex.Unlock()

	if contextCreated {
		return nil, nil, fmt.Errorf("oto: context is already created")
	}
	contextCreated = true

	var bufferSizeInBytes int
	if options.BufferSize != 0 {
		// The underlying driver always uses 32bit floats.
		bytesPerSample := options.ChannelCount * 4
		bytesPerSecond := options.SampleRate * bytesPerSample
		bufferSizeInBytes = int(int64(options.BufferSize) * int64(bytesPerSecond) / int64(time.Second))
		bufferSizeInBytes = bufferSizeInBytes / bytesPerSample * bytesPerSample
	}
	ctx, ready, err := newContext(options.SampleRate, options.ChannelCount, mux.Format(options.Format), bufferSizeInBytes, options.ApplicationName)
	if err != nil {
		return nil, nil, err
	}
	return &Context{context: ctx}, ready, nil
}

// NewPlayer creates a new, ready-to-use Player belonging to the Context.
// It is safe to create multiple players.
//
// The returned player must be kept reachable as long as it should keep playing.
// A player is closed when it becomes unreachable, even in the middle of playing.
//
// The format of r is as follows:
//
//	[data]      = [sample 1] [sample 2] [sample 3] ...
//	[sample *]  = [channel 1] [channel 2] ...
//	[channel *] = [byte 1] [byte 2] ...
//
// Byte ordering is little endian.
//
// A player has some amount of an underlying buffer.
// Read data from r is queued to the player's underlying buffer.
// The underlying buffer is consumed by its playing.
// Then, r's position and the current playing position don't necessarily match.
// If you want to seek the position of r, call the player's Seek function,
// which also clears the underlying buffer.
// If you want to stop using r e.g., you want to close r, call the player's PauseAndStopReading function.
//
// You cannot share r by multiple players.
//
// The returned player implements Player, BufferSizeSetter, and io.Seeker.
// You can modify the buffer size of a player by the SetBufferSize function.
// A small buffer size is useful if you want to play a real-time PCM for example.
// Note that the audio quality might be affected if you modify the buffer size.
//
// If r does not implement io.Seeker, the returned player's Seek returns an error.
//
// NewPlayer is concurrent-safe.
//
// All the functions of a Player returned by NewPlayer are concurrent-safe.
func (c *Context) NewPlayer(r io.Reader) *Player {
	return &Player{
		player: c.context.mux.NewPlayer(r),
	}
}

// Suspend suspends the entire audio play.
//
// Suspend does nothing after Close.
//
// Suspend is concurrent-safe.
func (c *Context) Suspend() error {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()

	if c.closed {
		return nil
	}
	return c.context.Suspend()
}

// Resume resumes the entire audio play, which was suspended by Suspend.
//
// Resume does nothing after Close.
//
// Resume is concurrent-safe.
func (c *Context) Resume() error {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()

	if c.closed {
		return nil
	}
	return c.context.Resume()
}

// Err returns an error that occurred in the audio driver, if any.
// Errors reported by Err are fatal: once Err returns a non-nil error,
// this context is no longer usable. Close it to create another.
//
// Err is concurrent-safe.
func (c *Context) Err() error {
	return c.context.Err()
}

// DeviceSampleRate returns the sample rate the audio device runs at, and whether
// the driver knows it. When it differs from the sample rate of the context, the
// system converts the sound to the device's rate before the device plays it.
//
// What it reports depends on the driver:
//
//   - WASAPI (Windows): the sample rate of the device's shared mix format.
//     Windows converts the sound to it.
//   - PulseAudio (Linux and BSD): the sample rate of the sink the sound plays
//     to. The sound server converts the sound to it. DeviceSampleRate asks the
//     server each time it is called.
//   - ALSA (Linux and BSD): the sample rate ALSA set up for the device oto
//     opened. A plugin device, such as the default one, can convert the sound
//     again, to a rate that is not reported.
//   - AudioQueue (macOS and iOS): the sample rate of the output device.
//
// The other drivers do not report it. DeviceSampleRate returns 0 and false
// when the rate is not known, before the context is ready, and after Close.
//
// DeviceSampleRate is concurrent-safe.
func (c *Context) DeviceSampleRate() (int, bool) {
	c.closeMu.RLock()
	defer c.closeMu.RUnlock()

	if c.closed {
		return 0, false
	}
	return c.context.DeviceSampleRate()
}

// Close closes the context. It stops the audio driver, and releases the audio
// device and the goroutines and threads the context uses. The context's players
// stop playing and reading their sources. Calls on them after Close are safe,
// and do nothing; so are calls on the context.
//
// Close waits for a read from a player's source in flight, if any, so no source
// is read after Close returns. Close must not be called from a source's Read.
//
// After Close returns, NewContext can be called again, for example with another
// sample rate or buffer size.
//
// On the platforms where a context cannot be closed yet, Close returns
// ErrCloseUnsupported and the context stays usable. On the other platforms the
// context is closed even if Close returns an error. Calling Close again returns
// nil.
//
// Close is concurrent-safe.
func (c *Context) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()

	if c.closed {
		return nil
	}

	err := c.context.Close()
	if errors.Is(err, ErrCloseUnsupported) {
		return err
	}
	c.closed = true
	c.context.mux.Close()

	contextCreationMutex.Lock()
	defer contextCreationMutex.Unlock()
	contextCreated = false

	return err
}

type atomicError struct {
	err error
	m   sync.Mutex
}

// Join records err in addition to the errors recorded so far. A nil err is
// ignored.
func (a *atomicError) Join(err error) {
	if err == nil {
		return
	}

	a.m.Lock()
	defer a.m.Unlock()
	a.err = errors.Join(a.err, err)
}

func (a *atomicError) Load() error {
	a.m.Lock()
	defer a.m.Unlock()
	return a.err
}
