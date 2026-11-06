// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/mababaNiubi/go-iec61850/cmd/internal/models/mms"
	mms "github.com/mababaNiubi/go-iec61850/mms"
)

func main() {
	address := flag.String("address", "127.0.0.1:8102", "TCP listen address")
	flag.Parse()
	device, err := mmsmodel.New()
	if err != nil {
		log.Fatal(err)
	}
	server, err := mms.NewServer(device, mms.Config{})
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Print(err)
		}
	}()
	fmt.Printf("MMS server listening on %s; domain=sample\n", listener.Addr())
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}
