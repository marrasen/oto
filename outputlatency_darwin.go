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

import "time"

// OutputLatency returns how long the sound the context has read from its
// players takes to be heard, as the device last reported it, and whether it
// has. It counts what the context queues and what the device holds: on
// macOS, the AudioQueue's buffers, the output device's buffer, and the
// device's own latency and safety offset. iOS reports none so far.
// It leaves out what each player buffers: see Player.BufferedSize.
//
// OutputLatency is concurrent-safe.
func (c *Context) OutputLatency() (time.Duration, bool) {
	return c.context.outputLatency()
}
