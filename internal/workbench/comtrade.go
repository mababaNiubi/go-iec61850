// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// The COMTRADE reader covers the 1991/1999/2013 configuration layout and the
// ASCII, BINARY, BINARY32 and FLOAT32 data encodings. Only the first sampling
// block is interpreted numerically; playback still follows sample order, so
// multi-rate captures animate correctly.
const (
	recordingChannelLimit = 512
	recordingValueLimit   = 4 << 20 // samples × channels, ~16 MB of float32
)

type chAnalog struct {
	id, phase, ccbm, unit string
	a, b                  float64
}
type chDigital struct {
	id, phase, ccbm string
}

func scanLines(raw []byte) []string {
	out := []string{}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) > 0 {
			out = append(out, string(line))
		}
	}
	return out
}

func fields(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

// parseCfgTime reads "DD,MM,yyyy,hh,mm,ss.ssssss" honouring the declared day
// order. Unspecified formats default to day-first.
func parseCfgTime(parts []string, dayFirst bool) (time.Time, error) {
	if len(parts) < 6 {
		return time.Time{}, fmt.Errorf("expected DD,MM,yyyy,hh,mm,ss.ssssss")
	}
	day, month := parts[0], parts[1]
	if !dayFirst {
		day, month = month, day
	}
	d, err1 := strconv.Atoi(day)
	mo, err2 := strconv.Atoi(month)
	y, err3 := strconv.Atoi(parts[2])
	h, err4 := strconv.Atoi(parts[3])
	mi, err5 := strconv.Atoi(parts[4])
	sec, err6 := parseFloat(parts[5])
	for _, e := range []error{err1, err2, err3, err4, err5, err6} {
		if e != nil {
			return time.Time{}, fmt.Errorf("malformed timestamp %v", parts)
		}
	}
	nanos := (sec - math.Trunc(sec)) * 1e9
	return time.Date(y, time.Month(mo), d, h, mi, int(sec), int(nanos), time.UTC), nil
}

func parseComtrade(cfg, dat []byte, name string) (*recording, error) {
	lines := scanLines(cfg)
	if len(lines) < 5 {
		return nil, fmt.Errorf("cfg needs a station header, two timestamps, a channel count and channel lines")
	}
	// The tail declares the data format and day order; scan it first so the
	// header timestamps can be interpreted correctly.
	format, dayFirst := "", true
	for _, line := range lines[3:] {
		f := fields(line)
		if len(f) != 1 {
			continue
		}
		switch {
		case strings.EqualFold(f[0], "ascii"):
			format = "ASCII"
		case strings.EqualFold(f[0], "binary"):
			format = "BINARY"
		case strings.EqualFold(f[0], "binary32"):
			format = "BINARY32"
		case strings.EqualFold(f[0], "float32"):
			format = "FLOAT32"
		case strings.Count(f[0], "/") == 2 && strings.ContainsAny(f[0], "dmy"):
			dayFirst = !strings.HasPrefix(strings.ToLower(f[0]), "mm/")
		}
	}
	if format == "" {
		return nil, fmt.Errorf("cfg does not declare ASCII, BINARY, BINARY32 or FLOAT32")
	}
	station := fields(lines[0])
	rev := ""
	if len(station) >= 3 {
		rev = station[2]
	}
	start, err := parseCfgTime(fields(lines[1]), dayFirst)
	if err != nil {
		return nil, fmt.Errorf("start time: %v", err)
	}
	if _, err = parseCfgTime(fields(lines[2]), dayFirst); err != nil {
		return nil, fmt.Errorf("trigger time: %v", err)
	}
	i := 3
	if rev == "2013" {
		if f := fields(lines[i]); len(f) >= 1 {
			if format == strings.ToUpper(f[0]) {
				i++ // 2013 declares ft right after the trigger time
			}
		}
	}
	if i >= len(lines) {
		return nil, fmt.Errorf("cfg ends before the channel count")
	}
	counts := fields(lines[i])
	i++
	total, analogTotal := 0, -1
	if len(counts) >= 2 {
		if analogTotal, err = strconv.Atoi(counts[0]); err != nil {
			return nil, fmt.Errorf("channel count: %v", err)
		}
		digitalTotal, e := strconv.Atoi(counts[1])
		if e != nil {
			return nil, fmt.Errorf("channel count: %v", e)
		}
		total = analogTotal + digitalTotal
	} else if total, err = strconv.Atoi(counts[0]); err != nil {
		return nil, fmt.Errorf("channel count: %v", err)
	}
	if total < 1 || total > recordingChannelLimit {
		return nil, fmt.Errorf("channel count must be between 1 and %d, got %d", recordingChannelLimit, total)
	}
	channels := []RecordingChannel{}
	analog := []chAnalog{}
	digital := []chDigital{}
	for len(channels) < total && i < len(lines) {
		f := fields(lines[i])
		switch {
		case len(f) >= 10:
			a, e1 := parseFloat(f[5])
			b, e2 := parseFloat(f[6])
			if e1 != nil || e2 != nil {
				return nil, fmt.Errorf("channel %q: bad scaling factors", f[1])
			}
			analog = append(analog, chAnalog{id: f[1], phase: f[2], ccbm: f[3], unit: f[4], a: a, b: b})
			channels = append(channels, RecordingChannel{Index: len(channels), ID: f[1], Phase: f[2], Unit: f[4], CCBM: f[3]})
		case len(f) >= 6:
			digital = append(digital, chDigital{id: f[1], phase: f[2], ccbm: f[3]})
			channels = append(channels, RecordingChannel{Index: len(channels), ID: f[1], Phase: f[2], CCBM: f[3], Digital: true})
		default:
			return nil, fmt.Errorf("cfg line %d: expected a channel definition, got %q", i+1, lines[i])
		}
		i++
	}
	if len(channels) != total {
		return nil, fmt.Errorf("cfg declares %d channels but defines %d", total, len(channels))
	}
	if analogTotal >= 0 && analogTotal != len(analog) {
		return nil, fmt.Errorf("cfg declares %d analog channels but defines %d", analogTotal, len(analog))
	}
	rate := 0.0
	for _, line := range lines[i:] {
		f := fields(line)
		if len(f) >= 2 {
			if r, e := parseFloat(f[0]); e == nil && r > 0 && rate == 0 {
				rate = r
			}
		}
	}
	rec := &recording{Recording: Recording{Name: name, Format: format, RateHz: rate, Start: start, Channels: channels},
		analog: make([][]float32, len(analog)), digital: make([][]bool, len(digital))}
	minimums := make([]float64, len(analog))
	maximums := make([]float64, len(analog))
	switch format {
	case "ASCII":
		err = readASCIIDat(dat, rec, analog, minimums, maximums)
	case "BINARY":
		err = readBinaryDat(dat, rec, analog, minimums, maximums, 2, true, true)
	case "BINARY32":
		err = readBinaryDat(dat, rec, analog, minimums, maximums, 4, true, true)
	case "FLOAT32":
		err = readBinaryDat(dat, rec, analog, minimums, maximums, 4, false, false)
	}
	if err != nil {
		return nil, err
	}
	rec.Samples = 0
	if len(rec.analog) > 0 {
		rec.Samples = len(rec.analog[0])
	} else if len(rec.digital) > 0 {
		rec.Samples = len(rec.digital[0])
	}
	if rec.Samples == 0 {
		return nil, fmt.Errorf("dat contains no samples")
	}
	for c := range analog {
		channels[c].Min = minimums[c]
		channels[c].Max = maximums[c]
	}
	if rec.lastMicros > 0 {
		rec.End = start.Add(time.Duration(rec.lastMicros) * time.Microsecond)
	} else {
		rec.End = start
	}
	return rec, nil
}

// readASCIIDat reads "sample,time,status…,analog…" rows. Timestamps are
// microseconds since the start of the capture; analog columns are raw counts
// scaled with each channel's a and b.
func readASCIIDat(dat []byte, rec *recording, defs []chAnalog, minimums, maximums []float64) error {
	nA, nD := len(defs), len(rec.digital)
	want := 2 + nD + nA
	rows := 0
	for _, line := range scanLines(dat) {
		f := fields(line)
		if len(f) != want {
			return fmt.Errorf("dat row needs %d fields, got %d: %q", want, len(f), line)
		}
		row := make([]float64, want)
		for c, cell := range f {
			v, err := parseFloat(cell)
			if err != nil {
				return fmt.Errorf("dat value %q: %v", cell, err)
			}
			row[c] = v
		}
		if row[1] > rec.lastMicros {
			rec.lastMicros = row[1]
		}
		for c := 0; c < nD; c++ {
			rec.digital[c] = append(rec.digital[c], row[2+c] != 0)
		}
		for c := 0; c < nA; c++ {
			v := defs[c].a*row[2+nD+c] + defs[c].b
			rec.analog[c] = append(rec.analog[c], float32(v))
			if rows == 0 || v < minimums[c] {
				minimums[c] = v
			}
			if rows == 0 || v > maximums[c] {
				maximums[c] = v
			}
		}
		rows++
		if rows*(nA+nD) > recordingValueLimit {
			return fmt.Errorf("recording exceeds %d channel samples", recordingValueLimit)
		}
	}
	return nil
}

// readBinaryDat reads the fixed-width BINARY, BINARY32 and FLOAT32 encodings:
// a u32 sample number, the timestamp, ceil(nD/16) status words, then nA
// samples. FLOAT32 timestamps and values are already seconds/engineering
// units; the integer encodings scale raw counts with a and b.
func readBinaryDat(dat []byte, rec *recording, defs []chAnalog, minimums, maximums []float64, width int, micros, scaled bool) error {
	nA, nD := len(defs), len(rec.digital)
	words := (nD + 15) / 16
	rowSize := 4 + 4 + 2*words + width*nA
	if rowSize <= 0 {
		return fmt.Errorf("empty binary row")
	}
	count := len(dat) / rowSize
	if count == 0 {
		return fmt.Errorf("dat holds no complete sample row")
	}
	if count*(nA+nD) > recordingValueLimit {
		return fmt.Errorf("recording exceeds %d channel samples", recordingValueLimit)
	}
	for c := range rec.analog {
		rec.analog[c] = make([]float32, count)
		minimums[c], maximums[c] = math.Inf(1), math.Inf(-1)
	}
	for c := range rec.digital {
		rec.digital[c] = make([]bool, count)
	}
	for n := 0; n < count; n++ {
		row := dat[n*rowSize:]
		var stamp float64
		if micros {
			stamp = float64(binary.LittleEndian.Uint32(row[4:8]))
		} else {
			stamp = float64(math.Float32frombits(binary.LittleEndian.Uint32(row[4:8]))) * 1e6
		}
		if stamp > rec.lastMicros {
			rec.lastMicros = stamp
		}
		for w := 0; w < words; w++ {
			word := binary.LittleEndian.Uint16(row[8+2*w:])
			for b := 0; b < 16; b++ {
				if c := w*16 + b; c < nD {
					rec.digital[c][n] = word&(1<<b) != 0
				}
			}
		}
		for c := 0; c < nA; c++ {
			off := 8 + 2*words + width*c
			var v float64
			switch {
			case width == 2:
				v = float64(int16(binary.LittleEndian.Uint16(row[off:])))
			case scaled:
				v = float64(int32(binary.LittleEndian.Uint32(row[off:])))
			default:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(row[off:])))
			}
			if scaled {
				v = defs[c].a*v + defs[c].b
			}
			rec.analog[c][n] = float32(v)
			if v < minimums[c] {
				minimums[c] = v
			}
			if v > maximums[c] {
				maximums[c] = v
			}
		}
	}
	return nil
}
