// Copyright 2022 The Oto Authors
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
	"sync/atomic"
	"time"

	"github.com/marrasen/oto/v3/internal/mux"
)

var errDeviceNotFound = errors.New("oto: device not found")

type context struct {
	sampleRate   int
	channelCount int

	mux *mux.Mux

	wasapiContext *wasapiContext
	winmmContext  *winmmContext
	nullContext   *nullContext

	ready chan struct{}
	err   atomicError
}

func newContext(sampleRate int, channelCount int, format mux.Format, bufferSizeInBytes int, _ string) (*context, chan struct{}, error) {
	ctx := &context{
		sampleRate:   sampleRate,
		channelCount: channelCount,
		mux:          mux.New(sampleRate, channelCount, format),
		ready:        make(chan struct{}),
	}

	// Initializing drivers might take some time. Do this asynchronously.
	go func() {
		defer close(ctx.ready)

		xc, err0 := newWASAPIContext(sampleRate, channelCount, ctx.mux, bufferSizeInBytes)
		if err0 == nil {
			ctx.wasapiContext = xc
			return
		}

		wc, err1 := newWinMMContext(sampleRate, channelCount, ctx.mux, bufferSizeInBytes)
		if err1 == nil {
			ctx.winmmContext = wc
			return
		}

		if errors.Is(err0, errDeviceNotFound) && errors.Is(err1, errDeviceNotFound) {
			ctx.nullContext = newNullContext(sampleRate, channelCount, ctx.mux)
			return
		}

		ctx.err.Join(fmt.Errorf("oto: initialization failed: WASAPI: %w, WinMM: %w", err0, err1))
	}()

	return ctx, ctx.ready, nil
}

func (c *context) Suspend() error {
	<-c.ready
	if c.wasapiContext != nil {
		return c.wasapiContext.Suspend()
	}
	if c.winmmContext != nil {
		return c.winmmContext.Suspend()
	}
	if c.nullContext != nil {
		return c.nullContext.Suspend()
	}
	return nil
}

func (c *context) Resume() error {
	<-c.ready
	if c.wasapiContext != nil {
		return c.wasapiContext.Resume()
	}
	if c.winmmContext != nil {
		return c.winmmContext.Resume()
	}
	if c.nullContext != nil {
		return c.nullContext.Resume()
	}
	return nil
}

func (c *context) DeviceSampleRate() (int, bool) {
	select {
	case <-c.ready:
	default:
		return 0, false
	}
	if c.wasapiContext != nil {
		return c.wasapiContext.DeviceSampleRate()
	}
	return 0, false
}

func (c *context) Close() error {
	<-c.ready
	if c.wasapiContext != nil {
		return c.wasapiContext.Close()
	}
	if c.winmmContext != nil {
		return c.winmmContext.Close()
	}
	if c.nullContext != nil {
		return c.nullContext.Close()
	}
	return nil
}

func (c *context) Err() error {
	if err := c.err.Load(); err != nil {
		return err
	}

	select {
	case <-c.ready:
	default:
		return nil
	}

	if c.wasapiContext != nil {
		return c.wasapiContext.Err()
	}
	if c.winmmContext != nil {
		return c.winmmContext.Err()
	}
	if c.nullContext != nil {
		return c.nullContext.Err()
	}
	return nil
}

type nullContext struct {
	suspended atomic.Bool

	// closing is closed by Close, and loopDone when loop returns.
	closing  chan struct{}
	loopDone chan struct{}
}

func newNullContext(sampleRate int, channelCount int, mux *mux.Mux) *nullContext {
	c := &nullContext{
		closing:  make(chan struct{}),
		loopDone: make(chan struct{}),
	}
	go c.loop(sampleRate, channelCount, mux)
	return c
}

func (c *nullContext) loop(sampleRate int, channelCount int, mux *mux.Mux) {
	defer close(c.loopDone)

	var buf32 [4096]float32
	sleep := time.Duration(float64(time.Second) * float64(len(buf32)) / float64(channelCount) / float64(sampleRate))
	for {
		if c.suspended.Load() {
			if !c.sleep(time.Second) {
				return
			}
			continue
		}

		mux.ReadFloat32s(buf32[:])
		if !c.sleep(sleep) {
			return
		}
	}
}

// sleep waits for d, and reports false if the context is closed meanwhile.
func (c *nullContext) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return true
	case <-c.closing:
		return false
	}
}

func (c *nullContext) Close() error {
	close(c.closing)
	<-c.loopDone
	return nil
}

func (c *nullContext) Suspend() error {
	c.suspended.Store(true)
	return nil
}

func (c *nullContext) Resume() error {
	c.suspended.Store(false)
	return nil
}

func (*nullContext) Err() error {
	return nil
}
