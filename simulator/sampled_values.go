// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"context"
	"fmt"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/sv"
)

// SampledValues describes one software SV source. Sample fills the entire
// seqData buffer for each sample, using Profile.Layout fields. A nil Sample
// produces zero-valued samples. The default SampleSync=0 explicitly declares
// an unsynchronized software clock. This scheduler uses ordinary Go timers.
type SampledValues struct {
	Profile          sv.Profile
	Source           [6]byte
	NominalFrequency uint16
	SampleSync       uint8
	Sample           func(context.Context, uint16, []byte) error
}

// RunSampledValues publishes to a caller-owned frame link. Cancellation stops
// the loop without closing that link. It can also be composed into Runtime.
func RunSampledValues(ctx context.Context, link sv.FrameIO, source SampledValues) error {
	frame, config, interval, err := prepareSV(source)
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("simulator: missing SV link")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var counter uint32
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			for i := range frame.PDU.ASDUs {
				a := &frame.PDU.ASDUs[i]
				a.SampleCount = uint16(counter)
				a.SampleSync = source.SampleSync
				clear(a.Data)
				if source.Sample != nil {
					if err := source.Sample(ctx, a.SampleCount, a.Data); err != nil {
						return err
					}
				}
				if a.RefreshTime != nil {
					raw, _ := data.UTC(time.Now()).Bytes()
					copy(a.RefreshTime[:], raw)
				}
				counter = (counter + 1) % config.CounterModulus
			}
			raw, err := sv.EncodeFrame(frame)
			if err != nil {
				return err
			}
			if err = link.WriteFrame(ctx, raw); err != nil {
				return err
			}
		}
	}
}
func prepareSV(source SampledValues) (sv.Frame, sv.SubscriptionConfig, time.Duration, error) {
	if source.Source == [6]byte{} {
		source.Source = [6]byte{2, 0, 0, 0, 0, 2}
	}
	if source.SampleSync > 2 {
		return sv.Frame{}, sv.SubscriptionConfig{}, 0, fmt.Errorf("simulator: invalid smpSynch")
	}
	frame, err := source.Profile.NewFrame(source.Source)
	if err != nil {
		return frame, sv.SubscriptionConfig{}, 0, err
	}
	cfg, err := source.Profile.SubscriptionConfig(sv.SubscriptionOptions{NominalFrequency: source.NominalFrequency, SyncPolicy: sv.AnySynchronization})
	if err != nil {
		return frame, cfg, 0, err
	}
	hz := source.NominalFrequency
	if hz == 0 {
		hz = 50
	}
	rate := float64(source.Profile.SampleRate)
	switch source.Profile.SampleMode {
	case 0:
		rate *= float64(hz)
	case 2:
		rate = 1 / rate
	}
	interval := time.Duration(float64(time.Second) * float64(source.Profile.ASDUCount) / rate)
	if interval < time.Microsecond || interval > 24*time.Hour {
		return frame, cfg, 0, fmt.Errorf("simulator: unsupported software sampling interval")
	}
	return frame, cfg, interval, nil
}
