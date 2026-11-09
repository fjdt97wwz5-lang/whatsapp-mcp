package wa

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
)

// analyzeOggOpus extracts the duration of an Ogg Opus file and builds a
// placeholder waveform for a WhatsApp voice note.
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}
	var lastGranule uint64
	var sampleRate uint32 = 48000
	var preSkip uint16
	var foundHead bool
	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}
		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}
		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeq := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])
		if i+27+numSegments >= len(data) {
			break
		}
		segTable := data[i+27 : i+27+numSegments]
		pageSize := 27 + numSegments
		for _, l := range segTable {
			pageSize += int(l)
		}
		if i+pageSize > len(data) {
			pageSize = len(data) - i
		}
		if !foundHead && pageSeq <= 1 {
			page := data[i : i+pageSize]
			if pos := bytes.Index(page, []byte("OpusHead")); pos >= 0 && pos+8+16 <= len(page) {
				p := pos + 8
				preSkip = binary.LittleEndian.Uint16(page[p+2 : p+4])
				sampleRate = binary.LittleEndian.Uint32(page[p+4 : p+8])
				foundHead = true
			}
		}
		if granulePos != 0 && granulePos != math.MaxUint64 {
			lastGranule = granulePos
		}
		i += pageSize
	}
	// Opus granule positions always count 48 kHz samples.
	_ = sampleRate
	if lastGranule > uint64(preSkip) {
		duration = uint32(math.Ceil(float64(lastGranule-uint64(preSkip)) / 48000.0))
	} else {
		duration = uint32(float64(len(data)) / 2000.0)
	}
	if duration < 1 {
		duration = 1
	} else if duration > 3600 {
		duration = 3600
	}
	return duration, placeholderWaveform(duration), nil
}

// placeholderWaveform generates a natural-looking 64 byte waveform.
func placeholderWaveform(duration uint32) []byte {
	const n = 64
	w := make([]byte, n)
	r := rand.New(rand.NewSource(int64(duration)))
	amp := 35.0
	freq := float64(min(int(duration), 120)) / 30.0
	for i := range w {
		pos := float64(i) / n
		v := amp*math.Sin(pos*math.Pi*freq*8) + (amp/2)*math.Sin(pos*math.Pi*freq*16)
		v += (r.Float64() - 0.5) * 15
		v = v*(0.7+0.3*math.Sin(pos*math.Pi)) + 50
		w[i] = byte(math.Max(0, math.Min(100, v)))
	}
	return w
}
