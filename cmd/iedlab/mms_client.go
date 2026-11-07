// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"time"

	mms "github.com/mababaNiubi/go-iec61850/mms"
)

func legacyMMSClient(args []string) error {
	flags := flag.NewFlagSet("mms-client", flag.ContinueOnError)
	address := flags.String("address", "127.0.0.1:8102", "MMS server address")
	domain := flags.String("domain", "sample", "MMS domain")
	item := flags.String("item", "int", "variable name")
	write := flags.String("write-int", "", "optional integer to write before reading")
	pdu := flags.Int("pdu", 65000, "proposed MMS PDU size")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mms.Dial(ctx, *address, mms.Config{MaxPDUSize: *pdu})
	if err != nil {
		return err
	}
	defer client.Close()
	id, err := client.Identify(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Server: %s / %s / %s\n", id.Vendor, id.Model, id.Revision)
	names, err := client.Names(ctx, mms.NamedVariable, mms.DomainScope, *domain)
	if err != nil {
		return err
	}
	fmt.Printf("Domain %s: %d variables\n", *domain, len(names))
	name := mms.DomainName(*domain, *item)
	if *write != "" {
		n, err := strconv.ParseInt(*write, 10, 64)
		if err != nil {
			return err
		}
		if err = client.Write(ctx, name, mms.Integer(n)); err != nil {
			return err
		}
	}
	value, err := client.Read(ctx, name)
	if err != nil {
		return err
	}
	fmt.Printf("%s = %s\n", name, value)
	if err = client.Conclude(ctx); err != nil {
		return err
	}
	return nil
}
