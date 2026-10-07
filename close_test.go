// Copyright 2026 The Oto Authors
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

package oto_test

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/marrasen/oto/v3"
)

// newTestContext creates a stereo float context, and closes it when the test
// finishes.
func newTestContext(t *testing.T, sampleRate int, bufferSize time.Duration) *oto.Context {
	t.Helper()

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: 2,
		Format:       oto.FormatFloat32LE,
		BufferSize:   bufferSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	t.Cleanup(func() {
		if err := ctx.Close(); err != nil {
			t.Error(err)
		}
	})
	return ctx
}

// playSilence plays d of silence on ctx, and waits until it has been played.
// Without an audio device, nothing reads the players, so it only logs that.
func playSilence(t *testing.T, ctx *oto.Context, sampleRate int, d time.Duration) {
	t.Helper()

	if err := ctx.Err(); err != nil {
		t.Logf("no audio device, so the sound is not played: %v", err)
		return
	}

	const bytesPerFrame = 2 * 4
	frames := int(int64(sampleRate) * int64(d) / int64(time.Second))
	p := ctx.NewPlayer(bytes.NewReader(make([]byte, frames*bytesPerFrame)))
	p.Play()

	deadline := time.Now().Add(d + 5*time.Second)
	for p.IsPlaying() {
		if time.Now().After(deadline) {
			t.Fatalf("the player at %d Hz did not finish playing", sampleRate)
		}
		time.Sleep(time.Millisecond)
	}
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
}

// endlessReader is a source of endless silence.
type endlessReader struct{}

func (endlessReader) Read(buf []byte) (int, error) {
	clear(buf)
	return len(buf), nil
}

func (endlessReader) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func TestNewContextWhileAnotherIsOpen(t *testing.T) {
	if _, _, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   48000,
		ChannelCount: 2,
		Format:       oto.FormatFloat32LE,
	}); err == nil {
		t.Fatal("NewContext while another context is open: got nil error; want an error")
	}
}

func TestCloseAndNewContext(t *testing.T) {
	const (
		sampleRate      = 48000
		otherSampleRate = 44100
	)

	playSilence(t, theContext, sampleRate, 100*time.Millisecond)

	old := theContext
	playing := old.NewPlayer(endlessReader{})
	playing.Play()

	// Close wakes a suspended driver up to end it.
	if err := old.Suspend(); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		if errors.Is(err, oto.ErrCloseUnsupported) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	// The other tests use theContext. A new one takes the place of the closed one
	// when this test finishes.
	t.Cleanup(func() {
		ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
			SampleRate:   sampleRate,
			ChannelCount: 2,
			Format:       oto.FormatFloat32LE,
		})
		if err != nil {
			t.Fatal(err)
		}
		<-ready
		theContext = ctx
	})

	// Closing again is harmless.
	if err := old.Close(); err != nil {
		t.Errorf("Close again: got %v; want nil", err)
	}

	// The closed context and its players do nothing, without blocking.
	if playing.IsPlaying() {
		t.Error("IsPlaying of a player of a closed context: got true; want false")
	}
	playing.Pause()
	playing.Play()
	if playing.IsPlaying() {
		t.Error("IsPlaying after Play on a closed context: got true; want false")
	}
	playing.SetVolume(0.5)
	playing.SetBufferSize(1024)
	if _, err := playing.Seek(0, io.SeekStart); err != nil {
		t.Error(err)
	}
	playing.PauseAndStopReading()
	if err := playing.Err(); err != nil {
		t.Error(err)
	}
	p := old.NewPlayer(endlessReader{})
	p.Play()
	if p.IsPlaying() {
		t.Error("IsPlaying of a new player of a closed context: got true; want false")
	}
	if err := old.Suspend(); err != nil {
		t.Error(err)
	}
	if err := old.Resume(); err != nil {
		t.Error(err)
	}

	// The goroutines of a context end when it is closed.
	goroutines := runtime.NumGoroutine()

	ctx := newTestContext(t, otherSampleRate, 50*time.Millisecond)
	playSilence(t, ctx, otherSampleRate, 100*time.Millisecond)
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > goroutines {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines after Close: got %d; want %d or fewer", runtime.NumGoroutine(), goroutines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
