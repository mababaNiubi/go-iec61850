// SPDX-License-Identifier: GPL-3.0-or-later
package scl

import "fmt"

type servicesXML struct {
	Reports *ReportSettings `xml:"ReportSettings"`
}

// ReportSettings preserves SCL Fix/Conf/Dyn engineering declarations. Only Dyn
// permits online writes. A missing attribute in ReportSettings defaults to Fix.
// A nil ReportSettings means the file makes no such capability declaration.
type ReportSettings struct {
	DataSet         string `xml:"datSet,attr"`
	ReportID        string `xml:"rptID,attr"`
	Options         string `xml:"optFields,attr"`
	BufferTime      string `xml:"bufTime,attr"`
	Triggers        string `xml:"trgOps,attr"`
	IntegrityPeriod string `xml:"intgPd,attr"`
}

// ReportSettings reads the effective AP/IED report capability declaration. A nil result means
// the file declares no ReportSettings; an error identifies invalid Fix/Conf/Dyn values.
func (s *System) ReportSettings(ied, ap string) (*ReportSettings, error) {
	p, err := s.find(ied, ap)
	if err != nil {
		return nil, err
	}
	var services *servicesXML
	for _, i := range s.doc.IEDs {
		if i.Name == ied {
			services = i.Services
			break
		}
	}
	if p.Services != nil {
		services = p.Services
	}
	if services == nil || services.Reports == nil {
		return nil, nil
	}
	r := *services.Reports
	for _, value := range []*string{&r.DataSet, &r.ReportID, &r.Options, &r.BufferTime, &r.Triggers, &r.IntegrityPeriod} {
		if *value == "" {
			*value = "Fix"
		}
		if *value != "Fix" && *value != "Conf" && *value != "Dyn" {
			return nil, fmt.Errorf("scl: invalid ReportSettings value %q", *value)
		}
	}
	return &r, nil
}

func (r *ReportSettings) online(field string) bool {
	if r == nil {
		return true
	}
	switch field {
	case "DatSet":
		return r.DataSet == "Dyn"
	case "RptID":
		return r.ReportID == "Dyn"
	case "OptFlds":
		return r.Options == "Dyn"
	case "BufTm":
		return r.BufferTime == "Dyn"
	case "TrgOps":
		return r.Triggers == "Dyn"
	case "IntgPd":
		return r.IntegrityPeriod == "Dyn"
	}
	return true
}
