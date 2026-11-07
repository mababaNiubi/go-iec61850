// SPDX-License-Identifier: GPL-3.0-or-later
package simulator

import (
	"context"
	"fmt"
	"strings"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
	"github.com/mababaNiubi/go-iec61850/sv"
)

// ModelSVSource publishes the model's current engineered dataset, with no
// inferred units or waveform. Editing MU acquisition values changes subsequent
// SV frames. All FCDA members are read in one snapshot. Quality preserves IEC
// flag numbers when converting MMS bit strings into SV's uint32 representation.
func ModelSVSource(model *scl.IED, profile sv.Profile) (SampledValues, error) {
	parts := strings.Split(profile.Reference, "/")
	if model == nil || len(parts) != 4 {
		return SampledValues{}, fmt.Errorf("simulator: invalid model SV source")
	}
	type fieldBinding struct {
		field sv.Field
		path  []int
	}
	var names []mms.ObjectName
	var fields [][]fieldBinding
	for _, member := range profile.Layout.Members {
		ld := member.Domain
		if ld == "local" {
			ld = parts[2]
		}
		path := strings.Split(member.Name, "$")
		if len(path) < 3 {
			return SampledValues{}, fmt.Errorf("simulator: invalid SV member %s", member.Name)
		}
		n, err := model.Lookup(ld, path[0], path[1], strings.Join(path[2:], "."))
		if err != nil {
			return SampledValues{}, err
		}
		spec, err := model.Type(context.Background(), n)
		if err != nil {
			return SampledValues{}, err
		}
		names = append(names, n)
		var bindings []fieldBinding
		for _, field := range member.Fields {
			_, indices, err := data.ResolveComponent(spec, field.Path)
			if err != nil {
				return SampledValues{}, err
			}
			bindings = append(bindings, fieldBinding{field, indices})
		}
		fields = append(fields, bindings)
	}
	source := SampledValues{Profile: profile.Clone()}
	source.Sample = func(ctx context.Context, _ uint16, raw []byte) error {
		values, err := model.ReadSnapshot(ctx, names...)
		if err != nil {
			return err
		}
		for member, bindings := range fields {
			for _, binding := range bindings {
				value, err := data.ValueAt(values[member], binding.path)
				if err != nil {
					return err
				}
				switch binding.field.Basic {
				case "Quality":
					q, err := ied.DecodeQuality(value)
					if err != nil {
						return err
					}
					value = data.Unsigned(uint64(q))
				case "Dbpos", "Check":
					width, err := value.BitLength()
					if err != nil || width > 32 {
						return fmt.Errorf("simulator: invalid bit-string SV source")
					}
					raw, _ := value.Bytes()
					var number uint64
					for _, b := range raw {
						number = number<<8 | uint64(b)
					}
					number >>= uint(len(raw)*8 - width)
					value = data.Unsigned(number)
				}
				if err := binding.field.Encode(raw, value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return source, nil
}
