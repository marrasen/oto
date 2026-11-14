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
	"math"
	"os"
	"testing"
	"time"

	"github.com/ebitengine/oto/v3"
)

// tone is a quiet endless 440 Hz tone, stereo float32 at 48000 Hz.
type tone struct{ frame int64 }

func (s *tone) Read(b []byte) (int, error) {
	n := len(b) / 8 * 8
	for i := 0; i < n; i += 8 {
		u := math.Float32bits(float32(0.1 * math.Sin(2*math.Pi*440*float64(s.frame)/48000)))
		for ch := 0; ch < 2; ch++ {
			o := i + 4*ch
			b[o], b[o+1], b[o+2], b[o+3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
		}
		s.frame++
	}
	return n, nil
}

// TestStarve checks whether the WASAPI stream's position keeps counting while
// the stream runs dry, or stops at the frames written. It plays a tone, so run
// it by hand: OTO_STARVE=1 go test -run TestStarve -v
// A test tool for ebitengine/oto#311, not for merging.
func TestStarve(t *testing.T) {
	if os.Getenv("OTO_STARVE") == "" {
		t.Skip("set OTO_STARVE=1 to run")
	}
	p := theContext.NewPlayer(&tone{})
	p.Play()
	defer p.Pause()

	for _, hold := range []time.Duration{600 * time.Millisecond, 3 * time.Second} {
		time.Sleep(2 * time.Second)
		samples, err := oto.Starve(theContext, hold, 50*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("hold %v:", hold)
		for _, s := range samples {
			t.Logf("  %6.0fms position=%8d written=%8d position-written=%+6d padding=%5d",
				float64(s.Since)/float64(time.Millisecond), s.Position, s.Written, s.Position-s.Written, s.Padding)
		}
		// Compare the readings from 100 ms into the hold to the end.
		var first oto.StarveSample
		for _, s := range samples {
			if s.Since >= 100*time.Millisecond {
				first = s
				break
			}
		}
		last := samples[len(samples)-1]
		moved := float64(last.Position-first.Position) / 48
		elapsed := float64(last.Since-first.Since) / float64(time.Millisecond)
		verdict := "FROZEN"
		if moved > elapsed/2 {
			verdict = "COUNTING"
		}
		t.Logf("RESULT hold %v: %s, position moved %.0f ms in %.0f ms, ends %+d frames from written, padding %d",
			hold, verdict, moved, elapsed, last.Position-last.Written, last.Padding)
		time.Sleep(time.Second)
		if err := theContext.Err(); err != nil {
			t.Fatalf("context error after the hold: %v", err)
		}
	}
}
