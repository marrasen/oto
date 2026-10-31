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

//go:build darwin && !ios

package oto

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	kAudioObjectSystemObject = 1

	kAudioObjectPropertyScopeGlobal = 0x676c6f62 // 'glob'
	kAudioObjectPropertyScopeOutput = 0x6f757470 // 'outp'
	kAudioObjectPropertyElementMain = 0

	kAudioHardwarePropertyDefaultOutputDevice = 0x644f7574 // 'dOut'

	kAudioDevicePropertyBufferFrameSize      = 0x6673697a // 'fsiz'
	kAudioDevicePropertyBufferFrameSizeRange = 0x66737a23 // 'fsz#'
	kAudioDevicePropertyNominalSampleRate    = 0x6e737274 // 'nsrt'
	kAudioDevicePropertyLatency              = 0x6c746e63 // 'ltnc'
	kAudioDevicePropertySafetyOffset         = 0x73616674 // 'saft'
	kAudioDevicePropertyStreams              = 0x73746d23 // 'stm#'

	kAudioStreamPropertyLatency = 0x6c746e63 // 'ltnc'
)

type _AudioObjectPropertyAddress struct {
	mSelector uint32
	mScope    uint32
	mElement  uint32
}

type _AudioValueRange struct {
	mMinimum float64
	mMaximum float64
}

var (
	_AudioObjectGetPropertyData        func(inObjectID uint32, inAddress *_AudioObjectPropertyAddress, inQualifierDataSize uint32, inQualifierData unsafe.Pointer, ioDataSize *uint32, outData unsafe.Pointer) int32
	_AudioObjectSetPropertyData        func(inObjectID uint32, inAddress *_AudioObjectPropertyAddress, inQualifierDataSize uint32, inQualifierData unsafe.Pointer, inDataSize uint32, inData unsafe.Pointer) int32
	_AudioObjectAddPropertyListener    func(inObjectID uint32, inAddress *_AudioObjectPropertyAddress, inListener uintptr, inClientData unsafe.Pointer) int32
	_AudioObjectRemovePropertyListener func(inObjectID uint32, inAddress *_AudioObjectPropertyAddress, inListener uintptr, inClientData unsafe.Pointer) int32
)

var (
	coreAudioOnce sync.Once
	coreAudioOK   bool
)

// initializeCoreAudio loads the functions of Core Audio that ask the output
// device of itself. It reports whether they loaded.
func initializeCoreAudio() bool {
	coreAudioOnce.Do(func() {
		ca, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&_AudioObjectGetPropertyData, ca, "AudioObjectGetPropertyData")
		purego.RegisterLibFunc(&_AudioObjectSetPropertyData, ca, "AudioObjectSetPropertyData")
		purego.RegisterLibFunc(&_AudioObjectAddPropertyListener, ca, "AudioObjectAddPropertyListener")
		purego.RegisterLibFunc(&_AudioObjectRemovePropertyListener, ca, "AudioObjectRemovePropertyListener")
		coreAudioOK = true
	})
	return coreAudioOK
}

// getProperty reads the property of object id at selector and scope into out,
// and returns the OSStatus.
func getProperty[T any](id uint32, selector, scope uint32, out *T) int32 {
	addr := _AudioObjectPropertyAddress{mSelector: selector, mScope: scope, mElement: kAudioObjectPropertyElementMain}
	size := uint32(unsafe.Sizeof(*out))
	return _AudioObjectGetPropertyData(id, &addr, 0, nil, &size, unsafe.Pointer(out))
}

// fitOutputDevice asks the default output device, which an AudioQueue plays
// to, to take frames at a time from this process, where frames is not 0.
// frames counts at sampleRate. The device's buffer is the process's own:
// other applications playing to the same device keep theirs.
//
// It returns how many frames, at sampleRate, the device then takes at a
// time, how long it takes to play a frame once it has taken it, and whether
// the device reports that.
func fitOutputDevice(frames int, sampleRate int) (int, time.Duration, bool, error) {
	if !initializeCoreAudio() {
		return 0, 0, false, fmt.Errorf("oto: loading CoreAudio failed")
	}
	var dev uint32
	if osstatus := getProperty(kAudioObjectSystemObject, kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal, &dev); osstatus != noErr {
		return 0, 0, false, fmt.Errorf("oto: reading the default output device failed: %d", osstatus)
	}
	if dev == 0 {
		return 0, 0, false, fmt.Errorf("oto: there is no default output device")
	}
	var rate float64
	if osstatus := getProperty(dev, kAudioDevicePropertyNominalSampleRate, kAudioObjectPropertyScopeGlobal, &rate); osstatus != noErr {
		return 0, 0, false, fmt.Errorf("oto: reading the output device's sample rate failed: %d", osstatus)
	}
	if rate <= 0 {
		return 0, 0, false, fmt.Errorf("oto: the output device's sample rate is %v", rate)
	}

	var setStatus int32 = noErr
	if frames > 0 {
		// The device counts its buffer at its own rate.
		want := float64(frames) * rate / float64(sampleRate)
		var r _AudioValueRange
		if getProperty(dev, kAudioDevicePropertyBufferFrameSizeRange, kAudioObjectPropertyScopeGlobal, &r) == noErr {
			want = min(max(want, r.mMinimum), r.mMaximum)
		}
		size := uint32(math.Ceil(want))
		addr := _AudioObjectPropertyAddress{mSelector: kAudioDevicePropertyBufferFrameSize, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain}
		// A device that refuses keeps the size it has, which is read below.
		setStatus = _AudioObjectSetPropertyData(dev, &addr, 0, nil, uint32(unsafe.Sizeof(size)), unsafe.Pointer(&size))
	}

	var size uint32
	if osstatus := getProperty(dev, kAudioDevicePropertyBufferFrameSize, kAudioObjectPropertyScopeGlobal, &size); osstatus != noErr || size == 0 {
		return 0, 0, false, fmt.Errorf("oto: reading the output device's buffer size failed: %d (setting it: %d)", osstatus, setStatus)
	}
	ioFrames := int(math.Ceil(float64(size) * float64(sampleRate) / rate))

	var latency, safety, stream, streamLatency uint32
	if getProperty(dev, kAudioDevicePropertyLatency, kAudioObjectPropertyScopeOutput, &latency) != noErr ||
		getProperty(dev, kAudioDevicePropertySafetyOffset, kAudioObjectPropertyScopeOutput, &safety) != noErr ||
		getProperty(dev, kAudioDevicePropertyStreams, kAudioObjectPropertyScopeOutput, &stream) != noErr || stream == 0 ||
		getProperty(stream, kAudioStreamPropertyLatency, kAudioObjectPropertyScopeGlobal, &streamLatency) != noErr {
		return ioFrames, 0, false, nil
	}
	held := time.Duration(float64(latency+safety+streamLatency) * float64(time.Second) / rate)
	return ioFrames, held, true, nil
}

var defaultOutputDeviceAddress = _AudioObjectPropertyAddress{mSelector: kAudioHardwarePropertyDefaultOutputDevice, mScope: kAudioObjectPropertyScopeGlobal, mElement: kAudioObjectPropertyElementMain}

var (
	outputDeviceListenerOnce sync.Once
	outputDeviceListener     uintptr

	// outputDeviceContext is the context outputDeviceChanged rebuilds, or nil.
	outputDeviceContext atomic.Pointer[context]
)

// watchOutputDevice rebuilds c's AudioQueue as the default output device
// changes, so the queue is fitted to the device it plays to next.
func watchOutputDevice(c *context) error {
	if !initializeCoreAudio() {
		return fmt.Errorf("oto: loading CoreAudio failed")
	}
	outputDeviceListenerOnce.Do(func() {
		outputDeviceListener = purego.NewCallback(outputDeviceChanged)
	})
	outputDeviceContext.Store(c)
	addr := defaultOutputDeviceAddress
	if osstatus := _AudioObjectAddPropertyListener(kAudioObjectSystemObject, &addr, outputDeviceListener, nil); osstatus != noErr {
		outputDeviceContext.CompareAndSwap(c, nil)
		return fmt.Errorf("oto: AudioObjectAddPropertyListener failed: %d", osstatus)
	}
	return nil
}

// unwatchOutputDevice stops what watchOutputDevice started for c.
func unwatchOutputDevice(c *context) error {
	if !outputDeviceContext.CompareAndSwap(c, nil) {
		return nil
	}
	addr := defaultOutputDeviceAddress
	if osstatus := _AudioObjectRemovePropertyListener(kAudioObjectSystemObject, &addr, outputDeviceListener, nil); osstatus != noErr {
		return fmt.Errorf("oto: AudioObjectRemovePropertyListener failed: %d", osstatus)
	}
	return nil
}

func outputDeviceChanged(inObjectID uint32, inNumberAddresses uint32, inAddresses unsafe.Pointer, inClientData unsafe.Pointer) int32 {
	if c := outputDeviceContext.Load(); c != nil {
		c.restartFromNotification(true)
	}
	return noErr
}
