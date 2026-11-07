// SPDX-License-Identifier: GPL-3.0-or-later
// scdinspect resolves engineered paths locally, without opening a network connection.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"strings"

	"github.com/mababaNiubi/go-iec61850/scl"
)

func runInspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	file := flags.String("file", "", "path to an SCD, ICD or CID file")
	ied := flags.String("ied", "", "source IED name (omit to list access points)")
	ap := flags.String("ap", "", "source access point name")
	ref := flags.String("ref", "", "look up one full LD/LN.DO.DA reference with -fc")
	fc := flags.String("fc", "", "functional constraint, e.g. ST, MX, CO, CF")
	match := flags.String("match", "", "limit the point listing to references containing this text")
	inputs := flags.Bool("inputs", false, "list GOOSE ExtRef bindings of the selected receiver access point")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("supply -file path/to/model.scd")
	}
	sys, err := scl.ParseFile(*file)
	if err != nil {
		return err
	}
	if *ied == "" && *ap == "" {
		if *inputs || *ref != "" || *fc != "" || *match != "" {
			return fmt.Errorf("point queries require -ied and -ap")
		}
		for _, p := range sys.AccessPoints() {
			fmt.Printf("%s/%s  server=%t\n", p.IED, p.AP, p.HasServer)
		}
		return nil
	}
	if *ied == "" || *ap == "" {
		return fmt.Errorf("specify both -ied and -ap")
	}
	if *inputs {
		if *ref != "" || *fc != "" || *match != "" {
			return fmt.Errorf("-inputs and point queries are separate modes")
		}
		refs, err := sys.GOOSEInputs(*ied, *ap)
		if err != nil {
			return err
		}
		for _, in := range refs {
			fmt.Printf("input %s -> source=%s GSE=%s/%s.%s data=%s/%s.%s\n", in.IntAddr, in.SourceIED, in.ControlLD, in.ControlLN, in.ControlBlock, in.DataLD, in.DataLN, in.DataPath)
		}
		return nil
	}
	d, err := sys.Device(*ied, *ap)
	if err != nil {
		return err
	}
	if *ref != "" {
		n, err := d.Resolve(*ref, *fc)
		if err != nil {
			return err
		}
		t, err := d.Type(context.Background(), n)
		if err != nil {
			return err
		}
		fmt.Printf("%s [%s] -> %s (%s)\n", *ref, *fc, n, t.Kind)
		return nil
	}
	fmt.Printf("IED %s/%s  MMS %s\n", *ied, *ap, d.MMSAddress())
	for _, p := range d.Points() {
		if (*fc == "" || p.FC == *fc) && strings.Contains(p.Reference, *match) {
			fmt.Printf("%s [%s] -> %s\n", p.Reference, p.FC, p.Name)
		}
	}
	for _, set := range d.DataSets() {
		fmt.Println("dataset", set.Name)
		for i, m := range set.Variables {
			fmt.Printf("  [%d] %s\n", i, m.Name)
		}
	}
	for _, g := range d.Streams() {
		i := g.Info()
		vlan := "untagged"
		if i.VLAN != nil {
			vlan = fmt.Sprintf("VID=%d PCP=%d", i.VLAN.ID, i.VLAN.Priority)
		}
		fmt.Printf("GOOSE %s dataset=%s goID=%s confRev=%d MAC=%s APPID=0x%04x %s interval=%s..%s\n",
			i.Reference, i.DataSet, i.GoID, i.ConfRev, net.HardwareAddr(i.Destination[:]), i.APPID, vlan, i.MinTime, i.MaxTime)
	}
	return nil
}
