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

//go:build ((linux && !android) || freebsd || netbsd) && !nintendosdk && !playstation5

package oto

import (
	"bytes"
	"testing"
	"time"

	"github.com/marrasen/oto/v3/internal/mux"
)

// The ALSA backend is opened directly, as NewContext prefers PulseAudio.
func TestALSACloseAndOpen(t *testing.T) {
	for _, sampleRate := range []int{48000, 44100} {
		m := mux.New(sampleRate, 2, mux.FormatFloat32LE)
		c, err := newALSAContextImpl(sampleRate, 2, m, 0)
		if err != nil {
			m.Close()
			t.Skipf("no ALSA device: %v", err)
		}
		if rate, ok := c.DeviceSampleRate(); !ok || rate <= 0 {
			t.Errorf("DeviceSampleRate() = %d, %t; want a rate above zero, true", rate, ok)
		}

		// Play 100 ms of silence.
		p := m.NewPlayer(bytes.NewReader(make([]byte, sampleRate/10*2*4)))
		p.Play()
		deadline := time.Now().Add(5 * time.Second)
		for p.IsPlaying() {
			if time.Now().After(deadline) {
				t.Fatalf("the player at %d Hz did not finish playing", sampleRate)
			}
			time.Sleep(time.Millisecond)
		}

		// Close ends the goroutine writing to the device, even while suspended.
		if err := c.Suspend(); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-c.loopDone:
		default:
			t.Fatal("the goroutine writing to the device did not end")
		}
		if err := c.Err(); err != nil {
			t.Errorf("Err after Close: got %v; want nil", err)
		}
		m.Close()
	}
}
