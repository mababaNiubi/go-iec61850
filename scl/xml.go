// SPDX-License-Identifier: GPL-3.0-or-later

// Package scl maps a selected SCL server's data model, static datasets and
// ordinary GOOSE controls to the mms and goose packages.
package scl

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"
)

const namespace = "http://www.iec.ch/61850/2003/SCL"

// System retains the engineering declarations. Device creates an independent
// runtime for one server access point each time it is called.
type System struct {
	doc     document
	lnTypes map[string]lnType
	doTypes map[string]doType
	daTypes map[string]daType
	enums   map[string]enumType
}

type document struct {
	XMLName     xml.Name        `xml:"http://www.iec.ch/61850/2003/SCL SCL"`
	Version     string          `xml:"version,attr"`
	Revision    string          `xml:"revision,attr"`
	Release     string          `xml:"release,attr"`
	Substations []substationXML `xml:"Substation"`
	IEDs        []xmlIED        `xml:"IED"`
	Connections []connection    `xml:"Communication>SubNetwork>ConnectedAP"`
	Types       templates       `xml:"DataTypeTemplates"`
}
type xmlIED struct {
	Name string        `xml:"name,attr"`
	APs  []accessPoint `xml:"AccessPoint"`
}
type accessPoint struct {
	ClientNodes []logicalNode `xml:"LN"`
	Name        string        `xml:"name,attr"`
	Server      *struct {
		Devices []logicalDevice `xml:"LDevice"`
	} `xml:"Server"`
}
type logicalDevice struct {
	Inst  string        `xml:"inst,attr"`
	Name  string        `xml:"ldName,attr"`
	Zero  []logicalNode `xml:"LN0"`
	Nodes []logicalNode `xml:"LN"`
}
type logicalNode struct {
	Class    string         `xml:"lnClass,attr"`
	Inst     string         `xml:"inst,attr"`
	Prefix   string         `xml:"prefix,attr"`
	Type     string         `xml:"lnType,attr"`
	Objects  []instance     `xml:"DOI"`
	Sets     []setXML       `xml:"DataSet"`
	Blocks   []blockXML     `xml:"GSEControl"`
	Sampled  []svControlXML `xml:"SampledValueControl"`
	Inputs   []extRef       `xml:"Inputs>ExtRef"`
	Reports  []reportXML    `xml:"ReportControl"`
	Settings *settingXML    `xml:"SettingControl"`
}

func (n logicalNode) name() string { return n.Prefix + n.Class + n.Inst }

type instance struct {
	Name      string     `xml:"name,attr"`
	Index     string     `xml:"ix,attr"`
	ValueKind string     `xml:"valKind,attr"`
	Values    []initial  `xml:"Val"`
	SDI       []instance `xml:"SDI"`
	DAI       []instance `xml:"DAI"`
}
type initial struct {
	Text  string `xml:",chardata"`
	Group string `xml:"sGroup,attr"`
}
type templates struct {
	LN   []lnType   `xml:"LNodeType"`
	DO   []doType   `xml:"DOType"`
	DA   []daType   `xml:"DAType"`
	Enum []enumType `xml:"EnumType"`
}
type declaration struct {
	Name        string `xml:"name,attr"`
	Type        string `xml:"type,attr"`
	Count       string `xml:"count,attr"`
	Description string `xml:"desc,attr"`
}
type attribute struct {
	declaration
	Basic     string    `xml:"bType,attr"`
	FC        string    `xml:"fc,attr"`
	Values    []initial `xml:"Val"`
	Dchg      bool      `xml:"dchg,attr"`
	Qchg      bool      `xml:"qchg,attr"`
	Dupd      bool      `xml:"dupd,attr"`
	ValueKind string    `xml:"valKind,attr"`
}
type lnType struct {
	ID      string        `xml:"id,attr"`
	Class   string        `xml:"lnClass,attr"`
	Objects []declaration `xml:"DO"`
}
type doType struct {
	ID          string        `xml:"id,attr"`
	CDC         string        `xml:"cdc,attr"`
	Description string        `xml:"desc,attr"`
	Sub         []declaration `xml:"SDO"`
	Attrs       []attribute   `xml:"DA"`
	order       []orderedAttribute
}
type daType struct {
	ID    string      `xml:"id,attr"`
	Attrs []attribute `xml:"BDA"`
}
type enumType struct {
	ID      string `xml:"id,attr"`
	Entries []struct {
		Ord  string `xml:"ord,attr"`
		Name string `xml:",chardata"`
	} `xml:"EnumVal"`
}
type setXML struct {
	Name    string      `xml:"name,attr"`
	Members []memberXML `xml:"FCDA"`
}
type memberXML struct {
	LD     string `xml:"ldInst,attr"`
	Prefix string `xml:"prefix,attr"`
	Class  string `xml:"lnClass,attr"`
	Inst   string `xml:"lnInst,attr"`
	DO     string `xml:"doName,attr"`
	DA     string `xml:"daName,attr"`
	FC     string `xml:"fc,attr"`
	Index  string `xml:"ix,attr"`
}
type extRef struct {
	memberXML
	IED          string `xml:"iedName,attr"`
	Address      string `xml:"intAddr,attr"`
	Service      string `xml:"serviceType,attr"`
	SourceLD     string `xml:"srcLDInst,attr"`
	SourcePrefix string `xml:"srcPrefix,attr"`
	SourceClass  string `xml:"srcLNClass,attr"`
	SourceInst   string `xml:"srcLNInst,attr"`
	Block        string `xml:"srcCBName,attr"`
}
type blockXML struct {
	Name     string  `xml:"name,attr"`
	Type     string  `xml:"type,attr"`
	Set      string  `xml:"datSet,attr"`
	Revision uint32  `xml:"confRev,attr"`
	GoID     string  `xml:"appID,attr"`
	Fixed    bool    `xml:"fixedOffs,attr"`
	Security string  `xml:"securityEnable,attr"`
	Protocol *string `xml:"Protocol"`
}
type param struct {
	Type string `xml:"type,attr"`
	Text string `xml:",chardata"`
}
type timing struct {
	Unit       string `xml:"unit,attr"`
	Multiplier string `xml:"multiplier,attr"`
	Text       string `xml:",chardata"`
}
type gse struct {
	LD      string  `xml:"ldInst,attr"`
	Block   string  `xml:"cbName,attr"`
	Address []param `xml:"Address>P"`
	Min     *timing `xml:"MinTime"`
	Max     *timing `xml:"MaxTime"`
}
type connection struct {
	IED     string   `xml:"iedName,attr"`
	AP      string   `xml:"apName,attr"`
	Address []param  `xml:"Address>P"`
	GSE     []gse    `xml:"GSE"`
	SMV     []smvXML `xml:"SMV"`
}

// Parse reads one SCL document (up to 16 MiB). Foreign extension elements
// are skipped. The relevant template references are resolved by Device.
func Parse(r io.Reader) (*System, error) {
	const limit = 16 << 20
	if r == nil {
		return nil, fmt.Errorf("scl: missing reader")
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("scl: document exceeds 16 MiB")
	}
	// XML permits a UTF-8 BOM at byte zero. Consume only that signature;
	// non-whitespace text elsewhere must still be rejected by sclTokens.
	raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
	reader := &sclTokens{dec: xml.NewDecoder(bytes.NewReader(raw))}
	dec := xml.NewTokenDecoder(reader)
	var s System
	if err := dec.Decode(&s.doc); err != nil {
		return nil, fmt.Errorf("scl: %w", err)
	}
	for {
		token, err := reader.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := token.(type) {
		case xml.Comment, xml.ProcInst:
		case xml.CharData:
			if strings.TrimSpace(string(t)) == "" {
				continue
			}
			return nil, fmt.Errorf("scl: unexpected trailing text %.24q", string(t))
		default:
			return nil, fmt.Errorf("scl: multiple document elements")
		}
	}
	if len(s.doc.IEDs) == 0 && len(s.doc.Substations) == 0 {
		return nil, fmt.Errorf("scl: no IED declarations")
	}
	if s.lnTypes, err = index(s.doc.Types.LN, func(t lnType) string { return t.ID }); err != nil {
		return nil, err
	}
	if s.doTypes, err = index(s.doc.Types.DO, func(t doType) string { return t.ID }); err != nil {
		return nil, err
	}
	if s.daTypes, err = index(s.doc.Types.DA, func(t daType) string { return t.ID }); err != nil {
		return nil, err
	}
	if s.enums, err = index(s.doc.Types.Enum, func(t enumType) string { return t.ID }); err != nil {
		return nil, err
	}
	seenIED := map[string]bool{}
	for _, ied := range s.doc.IEDs {
		if !identifier(ied.Name) || seenIED[ied.Name] {
			return nil, fmt.Errorf("scl: duplicate or invalid IED %q", ied.Name)
		}
		seenIED[ied.Name] = true
		if _, err := index(ied.APs, func(a accessPoint) string { return a.Name }); err != nil {
			return nil, fmt.Errorf("scl: IED %s: %w", ied.Name, err)
		}
	}
	return &s, nil
}

// ParseFile opens and closes an SCD, ICD or CID file using the same SCL parser.
func ParseFile(path string) (*System, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func index[T any](items []T, key func(T) string) (map[string]T, error) {
	out := make(map[string]T, len(items))
	for _, item := range items {
		id := key(item)
		if _, ok := out[id]; id == "" || ok {
			return nil, fmt.Errorf("scl: missing or duplicate identifier %q", id)
		}
		out[id] = item
	}
	return out, nil
}

// The XML decoder matches struct fields by local name. Filter foreign
// subtrees before decoding so a vendor extension cannot shadow an SCL field.
type sclTokens struct {
	dec            *xml.Decoder
	depth, foreign int
}

func (r *sclTokens) Token() (xml.Token, error) {
	for {
		t, err := r.dec.Token()
		if err != nil {
			return nil, err
		}
		switch e := t.(type) {
		case xml.CharData:
			if r.depth == 0 && strings.TrimSpace(string(e)) != "" {
				return nil, fmt.Errorf("scl: text outside document element")
			}
		case xml.Directive:
			return nil, fmt.Errorf("scl: XML directives are unsupported")
		case xml.StartElement:
			r.depth++
			if r.depth > 96 {
				return nil, fmt.Errorf("scl: excessive XML nesting")
			}
			if r.depth == 1 && e.Name.Space != namespace {
				return nil, fmt.Errorf("scl: unexpected root namespace %q", e.Name.Space)
			}
			if r.foreign != 0 || e.Name.Space != namespace {
				r.foreign++
				continue
			}
			// SCL attributes are unqualified. Ignore namespace declarations and extensions.
			attrs := e.Attr[:0]
			seen := make(map[string]bool)
			for _, a := range e.Attr {
				if a.Name.Space == "" && a.Name.Local != "xmlns" {
					if seen[a.Name.Local] {
						return nil, fmt.Errorf("scl: duplicate XML attribute %q", a.Name.Local)
					}
					seen[a.Name.Local] = true
					attrs = append(attrs, a)
				}
			}
			e.Attr = attrs
			return e, nil
		case xml.EndElement:
			r.depth--
			if r.foreign != 0 {
				r.foreign--
				continue
			}
		}
		if r.foreign == 0 {
			return t, nil
		}
	}
}

// AccessPoint identifies an engineered access point and whether it hosts a local server.
type AccessPoint struct {
	IED, AP   string
	HasServer bool
}

func (s *System) AccessPoints() []AccessPoint {
	var points []AccessPoint
	for _, i := range s.doc.IEDs {
		for _, a := range i.APs {
			points = append(points, AccessPoint{IED: i.Name, AP: a.Name, HasServer: a.Server != nil})
		}
	}
	return points
}
func (s *System) find(ied, ap string) (accessPoint, error) {
	for _, i := range s.doc.IEDs {
		if i.Name == ied {
			for _, a := range i.APs {
				if a.Name == ap {
					return a, nil
				}
			}
		}
	}
	return accessPoint{}, fmt.Errorf("scl: access point %s/%s not found", ied, ap)
}
