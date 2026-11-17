// SPDX-License-Identifier: GPL-3.0-or-later
package workbench

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const asciiCFG = "STATION1,DEV1,2013\r\n" +
	"01,01,2024,00,00,00.000000\r\n" +
	"01,01,2024,00,00,00.100000\r\n" +
	"ASCII\r\n" +
	"2,1\r\n" +
	"1,Ia,A,440V,A,0.5,10,0,-32767,32767,0,1,S\r\n" +
	"2,Ua,A,440V,V,0.1,100,0,-32767,32767,0,1,S\r\n" +
	"1,Brk,A,Q01,1,0\r\n" +
	"50\r\n" +
	"1\r\n" +
	"1000,5\r\n" +
	"0\r\n" +
	"dd/mm/yyyy\r\n"

const asciiDAT = "1,0,0,20,1000\r\n" +
	"2,1000,1,40,2000\r\n" +
	"3,2000,0,60,3000\r\n" +
	"4,3000,1,80,4000\r\n" +
	"5,4000,0,100,5000\r\n"

func TestParseComtradeASCII(t *testing.T) {
	rec, err := parseComtrade([]byte(asciiCFG), []byte(asciiDAT), "fault.cfg")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Format != "ASCII" || rec.RateHz != 1000 || rec.Samples != 5 || len(rec.Channels) != 3 {
		t.Fatalf("header mismatch: %+v", rec.Recording)
	}
	// Engineering values apply a·raw+b per channel.
	wantIa := []float32{20, 30, 40, 50, 60}
	for n, v := range rec.analog[0] {
		if v != wantIa[n] {
			t.Fatalf("Ia[%d] = %v, want %v", n, v, wantIa[n])
		}
	}
	if rec.analog[1][0] != 200 || rec.analog[1][4] != 600 {
		t.Fatalf("Ua scaling wrong: %v", rec.analog[1])
	}
	wantBrk := []bool{false, true, false, true, false}
	for n, v := range rec.digital[0] {
		if v != wantBrk[n] {
			t.Fatalf("Brk[%d] = %v, want %v", n, v, wantBrk[n])
		}
	}
	if rec.Channels[0].Min != 20 || rec.Channels[0].Max != 60 {
		t.Fatalf("channel range not captured: %+v", rec.Channels[0])
	}
	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if !rec.Start.Equal(want) {
		t.Fatalf("start %v, want %v", rec.Start, want)
	}
	if !rec.End.Equal(want.Add(4 * time.Millisecond)) {
		t.Fatalf("end %v, want start+4ms", rec.End)
	}
}

func binaryCFG(ft string, count string) string {
	return "STATION2,DEV2,1999\r\n" +
		"01,01,2024,00,00,00.000000\r\n" +
		"01,01,2024,00,00,00.100000\r\n" +
		count + "\r\n" +
		"1,Ia,A,440V,A,1.0,0,0,-2147483648,2147483647,0,1,S\r\n" +
		"1,Brk,A,Q01,1,0\r\n" +
		"50\r\n" +
		"1\r\n" +
		"1000,3\r\n" +
		"0\r\n" +
		"dd/mm/yyyy\r\n" +
		ft + "\r\n"
}

func TestParseComtradeBinary32(t *testing.T) {
	// 3 rows of n, µs, one status word, one i32 analog sample.
	dat := make([]byte, 0, 3*14)
	for n := 0; n < 3; n++ {
		row := make([]byte, 14)
		binary.LittleEndian.PutUint32(row[0:], uint32(n+1))
		binary.LittleEndian.PutUint32(row[4:], uint32(n)*1000)
		binary.LittleEndian.PutUint16(row[8:], uint16(1)) // channel 0 asserted
		binary.LittleEndian.PutUint32(row[10:], uint32(10*(n+1)))
		dat = append(dat, row...)
	}
	rec, err := parseComtrade([]byte(binaryCFG("BINARY32", "2")), dat, "b32")
	if err != nil {
		t.Fatal(err)
	}
	if rec.analog[0][0] != 10 || rec.analog[0][1] != 20 || rec.analog[0][2] != 30 {
		t.Fatalf("scaled samples wrong: %v", rec.analog[0])
	}
	if !rec.digital[0][0] || !rec.digital[0][1] || !rec.digital[0][2] {
		t.Fatalf("status bits wrong: %v", rec.digital[0])
	}
}

func TestParseComtradeFloat32Seconds(t *testing.T) {
	dat := make([]byte, 0, 3*14)
	stamps := []float32{0, 0.001, 0.002}
	values := []float32{1.5, -2.5, 3.25}
	for n := 0; n < 3; n++ {
		row := make([]byte, 14)
		binary.LittleEndian.PutUint32(row[0:], uint32(n+1))
		binary.LittleEndian.PutUint32(row[4:], math.Float32bits(stamps[n]))
		binary.LittleEndian.PutUint16(row[8:], 0)
		binary.LittleEndian.PutUint32(row[10:], math.Float32bits(values[n]))
		dat = append(dat, row...)
	}
	rec, err := parseComtrade([]byte(binaryCFG("FLOAT32", "2")), dat, "f32")
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range values {
		if rec.analog[0][n] != want {
			t.Fatalf("sample %d = %v, want %v", n, rec.analog[0][n], want)
		}
	}
	if rec.lastMicros < 1999 || rec.lastMicros > 2001 {
		t.Fatalf("seconds timestamp not converted to µs: %v", rec.lastMicros)
	}
}

func TestRecordingPlaybackDrivesGenerator(t *testing.T) {
	m, id := testDevice(t)
	ctx := context.Background()
	rec, err := m.ImportRecording("fault.cfg", []byte(asciiCFG), []byte(asciiDAT))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100, Loop: true,
		Recording: rec.ID, Channel: 0,
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[float64]bool{}
	deadline := time.Now().Add(4 * time.Second)
	for len(seen) < 5 && time.Now().Before(deadline) {
		if v, ok := readPoint(t, m, id, measure).(float64); ok {
			seen[v] = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(seen) < 5 {
		t.Fatalf("playback did not cycle every scaled sample, saw %v", seen)
	}
	// The digital channel maps onto the DPC status feedback.
	if _, err = m.SetGenerator(ctx, id, GeneratorConfig{
		Reference: "BayIEDCTRL/XCBR1.Pos.stVal", FC: "ST", Mode: "sequence", IntervalMS: 100, Loop: true,
		Recording: rec.ID, Channel: 2,
	}); err != nil {
		t.Fatal(err)
	}
	states := map[string]bool{}
	deadline = time.Now().Add(4 * time.Second)
	for len(states) < 2 && time.Now().Before(deadline) {
		if v, ok := readPoint(t, m, id, "BayIEDCTRL/XCBR1.Pos.stVal").(string); ok {
			states[v] = true
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(states) < 2 {
		t.Fatal("digital playback never toggled the breaker status")
	}
}

func TestRemoveRecordingStopsPlayback(t *testing.T) {
	m, id := testDevice(t)
	rec, err := m.ImportRecording("fault.cfg", []byte(asciiCFG), []byte(asciiDAT))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SetGenerator(context.Background(), id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100,
		Recording: rec.ID, Channel: 0, Loop: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err = m.RemoveRecording(rec.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.Generators(id); len(list) != 0 {
		t.Fatalf("deleting the recording should stop its playback, %d left", len(list))
	}
	if _, err = m.SetGenerator(context.Background(), id, GeneratorConfig{
		Reference: measure, FC: "MX", Mode: "sequence", IntervalMS: 100,
		Recording: rec.ID, Channel: 0,
	}); err == nil {
		t.Fatal("playback of a removed recording must fail")
	}
}

func TestRecordingRoutes(t *testing.T) {
	m, _ := testDevice(t)
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	cfgPart, _ := form.CreateFormFile("cfg", "fault.cfg")
	cfgPart.Write([]byte(asciiCFG))
	datPart, _ := form.CreateFormFile("dat", "fault.dat")
	datPart.Write([]byte(asciiDAT))
	form.Close()
	res, err := server.Client().Post(server.URL+"/api/recordings", form.FormDataContentType(), body)
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	var rec Recording
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ID == "" || rec.Samples != 5 {
		t.Fatalf("unexpected recording: %+v", rec)
	}
	res, err = server.Client().Get(server.URL + "/api/recordings")
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	res.Body.Close()
	req, _ := http.NewRequest("DELETE", server.URL+"/api/recordings/"+rec.ID, nil)
	res, err = server.Client().Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatal(err, res)
	}
	res.Body.Close()
	if list := m.Recordings(); len(list) != 0 {
		t.Fatalf("expected no recordings left, got %d", len(list))
	}
}
