// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strconv"
	"time"

	mms "github.com/mababaNiubi/go-iec61850/mms"
)

func main() {
	address := flag.String("address", "127.0.0.1:8102", "MMS server address")
	domain := flag.String("domain", "sample", "MMS domain")
	item := flag.String("item", "int", "variable name")
	write := flag.String("write-int", "", "optional integer to write before reading")
	pdu := flag.Int("pdu", 65000, "proposed MMS PDU size")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mms.Dial(ctx, *address, mms.Config{MaxPDUSize: *pdu})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	id, err := client.Identify(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Server: %s / %s / %s\n", id.Vendor, id.Model, id.Revision)
	names, err := client.Names(ctx, mms.NamedVariable, mms.DomainScope, *domain)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Domain %s: %d variables\n", *domain, len(names))
	name := mms.DomainName(*domain, *item)
	if *write != "" {
		n, err := strconv.ParseInt(*write, 10, 64)
		if err != nil {
			log.Fatal(err)
		}
		if err = client.Write(ctx, name, mms.Integer(n)); err != nil {
			log.Fatal(err)
		}
	}
	value, err := client.Read(ctx, name)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s = %s\n", name, value)
	if err = client.Conclude(ctx); err != nil {
		log.Fatal(err)
	}
}
