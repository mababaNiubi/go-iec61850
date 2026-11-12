// SPDX-License-Identifier: GPL-3.0-or-later
package ied_test

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/ied"
	"github.com/mababaNiubi/go-iec61850/mms"
)

// Discover needs only an address, not the peer's SCL file. Treat partial
// discovery as usable evidence, while retaining its explicit diagnostics.
func ExampleClient_Discover() {
	client, closePeer := examplePeer()
	defer closePeer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	directory, err := client.Discover(ctx, ied.DiscoveryOptions{MaxObjects: 128})
	if err != nil {
		panic(err)
	}
	fmt.Println("domains:", directory.Domains)
	fmt.Println("datasets:", len(directory.DataSets))
	fmt.Println("complete:", !directory.Truncated && len(directory.Diagnostics) == 0)
	// Output:
	// domains: [BayIEDCTRL]
	// datasets: 1
	// complete: true
}

// ReadDataSet performs a confirmed current read independently of subscriptions.
// Keep source quality and time alongside the measurement; the result has no
// BRCB EntryID and cannot acknowledge or advance buffered report history.
func ExampleClient_ReadDataSet() {
	client, closePeer := examplePeer()
	defer closePeer()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	name := mms.DomainName("BayIEDCTRL", "LLN0$measurements")
	snapshot, err := client.ReadDataSet(ctx, name)
	if err != nil {
		panic(err)
	}
	leaves, err := snapshot.Leaves()
	if err != nil {
		panic(err)
	}
	for _, leaf := range leaves {
		if leaf.Variable.Name.Item != "MMXU1$MX$TotW$mag$f" {
			continue
		}
		watts, err := leaf.Value.Float()
		if err != nil {
			panic(err)
		}
		fmt.Printf("%s = %.1f W\n", leaf.Variable.Name, watts)
	}
	fmt.Println("FCD members:", len(snapshot.Entries), "typed leaves:", len(leaves))
	// Output:
	// BayIEDCTRL/MMXU1$MX$TotW$mag$f = 6900.0 W
	// FCD members: 1 typed leaves: 3
}

// examplePeer supplies the local endpoint used by both examples. A production
// client simply calls ied.Dial with its remote address and defers client.Close.
// The explicit declaration gives Leaves the names omitted from MMS Data itself.
func examplePeer() (*ied.Client, func()) {
	quality, err := ied.Quality(0).MMSValue()
	if err != nil {
		panic(err)
	}
	floatType := data.TypeOf(data.Float32(0))
	spec := data.TypeSpec{Kind: data.KindStructure, Components: []data.Component{
		{Name: "mag", Type: data.TypeSpec{Kind: data.KindStructure, Components: []data.Component{
			{Name: "f", Type: floatType},
		}}},
		{Name: "q", Type: data.TypeSpec{Kind: data.KindBitString, Length: 13}},
		{Name: "t", Type: data.TypeSpec{Kind: data.KindUTC}},
	}}
	name := mms.DomainName("BayIEDCTRL", "MMXU1$MX$TotW")
	model := mms.NewModel()
	value := data.Structure(data.Structure(data.Float32(6900)), quality,
		data.UTC(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err := model.Add(mms.Variable{Name: name, Type: spec, Value: value}); err != nil {
		panic(err)
	}
	server, err := mms.NewServer(model, mms.Config{})
	if err != nil {
		panic(err)
	}
	if err := server.AddNamedList(mms.NamedList{
		Name:      mms.DomainName("BayIEDCTRL", "LLN0$measurements"),
		Variables: []mms.VariableAccess{mms.Access(name)},
	}); err != nil {
		panic(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ied.Dial(ctx, listener.Addr().String(), ied.Config{})
	if err != nil {
		server.Close()
		<-served
		panic(err)
	}
	return client, func() {
		client.Close()
		server.Close()
		if err := <-served; err != nil {
			panic(err)
		}
	}
}
