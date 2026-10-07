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

package mux_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/marrasen/oto/v3/internal/mux"
)

// countingReader is a source that produces silence and counts its reads.
type countingReader struct {
	reads atomic.Int64
}

func (r *countingReader) Read(buf []byte) (int, error) {
	r.reads.Add(1)
	for i := range buf {
		buf[i] = 1 << 7
	}
	return len(buf), nil
}

func (r *countingReader) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func TestCloseStopsReading(t *testing.T) {
	m := mux.New(48000, 1, mux.FormatUnsignedInt8)
	src := &countingReader{}
	p := newPlayer(t, m, src)
	p.SetBufferSize(64)
	p.Play()
	waitForBufferedSize(t, p, 64)

	m.Close()

	if p.IsPlaying() {
		t.Error("IsPlaying after Close: got true; want false")
	}
	if p.IsRegistered() {
		t.Error("IsRegistered after Close: got true; want false")
	}

	// Reading from the mux empties the players' buffers, which would make the
	// mux read their sources again if it were still running.
	reads := src.reads.Load()
	buf := make([]float32, 256)
	for range 10 {
		m.ReadFloat32s(buf)
		time.Sleep(time.Millisecond)
	}
	for i, v := range buf {
		if v != 0 {
			t.Fatalf("ReadFloat32s after Close: buf[%d] = %v; want 0", i, v)
		}
	}
	if got := src.reads.Load(); got != reads {
		t.Errorf("reads after Close: got %d; want %d", got, reads)
	}
}

func TestPlayAfterCloseDoesNothing(t *testing.T) {
	m := mux.New(48000, 1, mux.FormatUnsignedInt8)
	src := &countingReader{}
	p := newPlayer(t, m, src)

	m.Close()

	p.Play()
	if p.IsPlaying() {
		t.Error("IsPlaying after Play: got true; want false")
	}
	if p.IsRegistered() {
		t.Error("IsRegistered after Play: got true; want false")
	}

	// A player made after Close does nothing either.
	q := newPlayer(t, m, src)
	q.Play()
	if q.IsPlaying() {
		t.Error("IsPlaying of a new player after Play: got true; want false")
	}

	// The other functions must not block or panic.
	p.Pause()
	p.PauseAndStopReading()
	p.Reset()
	p.SetVolume(0.5)
	p.SetBufferSize(128)
	if _, err := p.Seek(0, 0); err != nil {
		t.Error(err)
	}
	_ = p.BufferedSize()
	if err := p.Err(); err != nil {
		t.Error(err)
	}

	time.Sleep(10 * time.Millisecond)
	if got := src.reads.Load(); got != 0 {
		t.Errorf("reads: got %d; want 0", got)
	}
}

func TestCloseWaitsForOngoingRead(t *testing.T) {
	src := &gatedReader{
		gate:  make(chan struct{}),
		began: make(chan struct{}),
	}
	m := mux.New(48000, 2, mux.FormatSignedInt16LE)
	p := newPlayer(t, m, src)
	p.Play()
	<-src.began

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Close()
	}()

	select {
	case <-done:
		t.Fatal("Close returned while a read from the source was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(src.gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the read finished")
	}

	// The source can be closed safely now.
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := p.Err(); err != nil {
		t.Errorf("Err after Close: got %v; want nil", err)
	}
}

func TestCloseTwice(t *testing.T) {
	m := mux.New(48000, 2, mux.FormatSignedInt16LE)
	m.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.Close()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the second Close did not return")
	}
}
