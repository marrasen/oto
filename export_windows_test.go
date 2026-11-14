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

package oto

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// StarveSample is one reading of the WASAPI stream while it is starved.
type StarveSample struct {
	Since    time.Duration // since the hold began
	Position int64         // the stream's position, in frames
	Written  int64         // frames written to the stream since its position 0
	Padding  uint32        // frames written and not yet taken by the engine
}

// Starve holds the WASAPI render thread off for hold, so the stream runs dry,
// and reads the stream's position every step meanwhile. A test tool for
// ebitengine/oto#311, not for merging.
func Starve(c *Context, hold, step time.Duration) ([]StarveSample, error) {
	w := c.context.wasapiContext
	if w == nil {
		return nil, fmt.Errorf("the context does not use WASAPI")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return nil, err
	}
	defer windows.CoUninitialize()

	w.m.Lock()
	defer w.m.Unlock()
	if w.audioClock == nil {
		return nil, fmt.Errorf("the stream has no IAudioClock")
	}
	var samples []StarveSample
	start := time.Now()
	for {
		pos, _, err := w.audioClock.GetPosition()
		if err != nil {
			return samples, err
		}
		padding, err := w.client.GetCurrentPadding()
		if err != nil {
			return samples, err
		}
		samples = append(samples, StarveSample{
			Since:    time.Since(start),
			Position: int64(pos) * int64(w.sampleRate) / int64(w.clockFrequency),
			Written:  w.mixed - w.streamStart,
			Padding:  padding,
		})
		if time.Since(start) >= hold {
			return samples, nil
		}
		time.Sleep(step)
	}
}
