// Command wintest checks Player.UnplayedSize on a real machine. It is a
// test tool for ebitengine/oto#311 and is not part of the pull request.
//
//	go run ./internal/wintest report   # the delay while playing, pausing, suspending
//	go run ./internal/wintest tap      # press Enter on each click you hear
//	go run ./internal/wintest switch   # 60 s while you switch the output device
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	rate       = 48000
	frameBytes = 8 // stereo float32
)

// clicks is a source of a short 1 kHz click at the start of every second.
type clicks struct {
	frames atomic.Int64 // frames read so far
}

func (c *clicks) Read(b []byte) (int, error) {
	n := len(b) / frameBytes * frameBytes
	f := c.frames.Load()
	for i := 0; i < n; i += frameBytes {
		var v float32
		if t := f % rate; t < rate/100 {
			v = float32(0.4 * math.Sin(2*math.Pi*1000*float64(t)/rate))
		}
		u := math.Float32bits(v)
		for ch := 0; ch < 2; ch++ {
			o := i + 4*ch
			b[o], b[o+1], b[o+2], b[o+3] = byte(u), byte(u>>8), byte(u>>16), byte(u>>24)
		}
		f++
	}
	c.frames.Store(f)
	return n, nil
}

func ms(bytes int) float64 { return float64(bytes) / frameBytes / rate * 1000 }

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: wintest report|tap|switch")
		os.Exit(2)
	}
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{SampleRate: rate, ChannelCount: 2, Format: oto.FormatFloat32LE})
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	<-ready
	src := &clicks{}
	p := ctx.NewPlayer(src)
	p.Play()

	logLine := func(tag string) {
		fmt.Printf("%-9s t=%s buffered=%6.1fms unplayed=%6.1fms delay=%6.1fms err=%v\n", tag, time.Now().Format("05.000"),
			ms(p.BufferedSize()), ms(p.UnplayedSize()), ms(p.UnplayedSize()-p.BufferedSize()), ctx.Err())
	}
	every := func(tag string, d, step time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			time.Sleep(step)
			logLine(tag)
		}
	}

	switch os.Args[1] {
	case "report":
		every("play", 3*time.Second, 250*time.Millisecond)
		p.Pause()
		every("pause", time.Second, 100*time.Millisecond)
		p.Play()
		every("replay", 2*time.Second, 250*time.Millisecond)
		if err := ctx.Suspend(); err != nil {
			fmt.Println("ERROR suspend", err)
		}
		every("suspended", time.Second, 250*time.Millisecond)
		if err := ctx.Resume(); err != nil {
			fmt.Println("ERROR resume", err)
		}
		every("resumed", 2*time.Second, 100*time.Millisecond)
		for i := 0; i < 5; i++ {
			ctx.Suspend()
			time.Sleep(time.Duration(100+100*i) * time.Millisecond)
			ctx.Resume()
			time.Sleep(300 * time.Millisecond)
			logLine("cycle")
		}
	case "tap":
		tap(p, src)
	case "switch":
		fmt.Println("Switch the output now: turn the earbuds off and on, or change the default device.")
		every("switch", 60*time.Second, time.Second)
	default:
		fmt.Println("unknown mode", os.Args[1])
		os.Exit(2)
	}
	fmt.Println("DONE", ctx.Err())
}

// tap asks for Enter on each click heard. For each press it takes the frame
// playing now, as the bytes read less UnplayedSize, and as the bytes read
// less BufferedSize, and how far that frame is past the nearest click.
func tap(p *oto.Player, src *clicks) {
	fmt.Println("Press Enter on each click you hear, 20 times. The first 3 presses are practice.")
	var withUnplayed, withBuffered []float64
	in := bufio.NewScanner(os.Stdin)
	for i := 0; i < 23 && in.Scan(); i++ {
		read := src.frames.Load() * frameBytes
		u := float64(read-int64(p.UnplayedSize())) / frameBytes
		b := float64(read-int64(p.BufferedSize())) / frameBytes
		off := func(f float64) float64 { return (f - math.Round(f/rate)*rate) / rate * 1000 }
		if i >= 3 {
			withUnplayed = append(withUnplayed, off(u))
			withBuffered = append(withBuffered, off(b))
		}
		fmt.Printf("press %2d: unplayed %+6.0fms  buffered %+6.0fms\n", i+1, off(u), off(b))
	}
	median := func(v []float64) float64 {
		if len(v) == 0 {
			return math.NaN()
		}
		s := append([]float64(nil), v...)
		sort.Float64s(s)
		return s[len(s)/2]
	}
	fmt.Printf("TAP median offset past the click: with UnplayedSize %+.0f ms, with BufferedSize %+.0f ms, %d presses\n",
		median(withUnplayed), median(withBuffered), len(withUnplayed))
}
