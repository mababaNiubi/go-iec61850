// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import "encoding/xml"

type orderedAttribute struct {
	attribute
	sub bool
}

// Keep DA/SDO declaration order while preserving the existing MMS compiler's
// indexed views. SV has no per-field BER tags: reordering siblings would shift
// offsets even if their MMS structure types happened to look equivalent.
func (t *doType) UnmarshalXML(dec *xml.Decoder, start xml.StartElement) error {
	*t = doType{}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "id":
			t.ID = a.Value
		case "cdc":
			t.CDC = a.Value
		case "desc":
			t.Description = a.Value
		}
	}
	for {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		switch e := token.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "DA":
				var a attribute
				if err := dec.DecodeElement(&a, &e); err != nil {
					return err
				}
				t.Attrs = append(t.Attrs, a)
				t.order = append(t.order, orderedAttribute{attribute: a})
			case "SDO":
				var d declaration
				if err := dec.DecodeElement(&d, &e); err != nil {
					return err
				}
				t.Sub = append(t.Sub, d)
				t.order = append(t.order, orderedAttribute{attribute: attribute{declaration: d}, sub: true})
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if e.Name == start.Name {
				return nil
			}
		}
	}
}
