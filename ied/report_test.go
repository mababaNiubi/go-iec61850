// SPDX-License-Identifier: GPL-3.0-or-later
package ied

import (
	"errors"
	"testing"

	"github.com/mababaNiubi/go-iec61850/mms"
)

func rawReport(sequence uint8) mms.InformationReport {
	list := mms.VMDName("RPT")
	options, _ := mms.BitString([]byte{0x48, 0x80}, 10)
	inclusion, _ := mms.BitString([]byte{0x80}, 1)
	values := []mms.Value{mms.VisibleString("Events"), options, mms.Unsigned(uint64(sequence)), mms.VisibleString("LD/LLN0$Events"), mms.Unsigned(1), inclusion, mms.Boolean(true)}
	report := mms.InformationReport{List: &list}
	for _, value := range values {
		report.Results = append(report.Results, mms.ReadResult{Value: value})
	}
	return report
}
func TestReportAtomicityRevisionAndSequence(t *testing.T) {
	name := mms.DomainName("LD", "GGIO1$ST$Ind1$stVal")
	decoder, err := NewReportDecoder(ReportConfig{ID: "Events", DataSet: "LD/LLN0$Events", ConfRev: 1, Members: []mms.VariableAccess{mms.Access(name)}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := decoder.Decode(rawReport(255))
	if err != nil || len(first.Entries) != 1 || first.Entries[0].Variable.Name != name {
		t.Fatal(first, err)
	}
	wrapped, err := decoder.Decode(rawReport(0))
	if err != nil || wrapped.Reset {
		t.Fatal(wrapped, err)
	}
	gap, err := decoder.Decode(rawReport(2))
	if err != nil || !gap.Reset {
		t.Fatal(gap, err)
	}
	bad := rawReport(3)
	bad.Results = bad.Results[:6]
	if _, err = decoder.Decode(bad); err == nil {
		t.Fatal("truncated report accepted")
	}
	bad = rawReport(3)
	bad.Results[4].Value = mms.Unsigned(2)
	if _, err = decoder.Decode(bad); err == nil {
		t.Fatal("changed ConfRev accepted")
	}
	for length := 0; length < 6; length++ {
		bad := rawReport(3)
		bad.Results = bad.Results[:length]
		if _, err := decoder.Decode(bad); err == nil {
			t.Fatal("short report accepted", length)
		}
	}
}
func TestTerminationRequiresObjectCommandAndOrigin(t *testing.T) {
	name := mms.DomainName("LD", "XCBR1$CO$Pos$Oper")
	origin := mms.Structure(mms.Integer(2), mms.OctetString([]byte("station")))
	value := mms.Structure(mms.Boolean(true), origin, mms.Unsigned(9))
	pending := termination{name: name, value: value, origin: origin, number: 9}
	report := mms.InformationReport{Variables: []mms.VariableAccess{mms.Access(name)}, Results: []mms.ReadResult{{Value: value}}}
	if ok, err := pending.match(report); !ok || err != nil {
		t.Fatal(ok, err)
	}
	report.Variables[0].Name.Domain = "OTHER"
	if ok, _ := pending.match(report); ok {
		t.Fatal("another device completed the command")
	}
	report.Variables = []mms.VariableAccess{mms.Access(mms.VMDName("LastApplError"))}
	report.Results = []mms.ReadResult{{Value: mms.Structure(mms.VisibleString(name.String()), mms.Integer(1), origin, mms.Unsigned(8), mms.Integer(10))}}
	if ok, _ := pending.match(report); ok {
		t.Fatal("another ctlNum rejected the command")
	}
	report.Results[0].Value = mms.Structure(mms.VisibleString(name.String()), mms.Integer(1), origin, mms.Unsigned(9), mms.Integer(10))
	if ok, err := pending.match(report); !ok || !errors.Is(err, ErrRejected) {
		t.Fatal(ok, err)
	}
}
