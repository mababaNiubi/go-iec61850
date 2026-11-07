// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mababaNiubi/go-iec61850/internal/sample"
	"github.com/mababaNiubi/go-iec61850/internal/workbench"
)

// loadWorkspace leaves selection to the browser when an SCD contains several
// IEDs. The headless simulator explicitly chooses its IED and access point.
func loadWorkspace(file string) (*workbench.Manager, workbench.Document, error) {
	m := workbench.New()
	if file == "" {
		return m, workbench.Document{}, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		m.Close()
		return nil, workbench.Document{}, err
	}
	doc, err := m.Import(file, raw)
	if err != nil {
		m.Close()
		return nil, doc, err
	}
	return m, doc, nil
}
func serveApp(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("http", "127.0.0.1:8080", "HTTP listen address")
	file := flags.String("file", "", "optional SCD/ICD/CID file to preload")
	if err := flags.Parse(args); err != nil {
		return err
	}
	m, _, err := loadWorkspace(*file)
	if err != nil {
		return err
	}
	defer m.Close()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Handler: m.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("IEC 61850 Workbench: http://%s\n", listener.Addr())
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		// Close SSE connections before waiting for HTTP shutdown.
		server.Close()
		<-done
		return nil
	}
}
func simulateApp(args []string) error {
	flags := flag.NewFlagSet("simulate", flag.ContinueOnError)
	file := flags.String("file", "", "SCD file; empty uses the built-in bay model")
	ied := flags.String("ied", "BayIED", "source IED")
	ap := flags.String("ap", "S1", "server access point")
	address := flags.String("listen", "127.0.0.1:8102", "MMS listen address")
	gooseMode := flags.String("goose", "memory", "off, memory or ethernet")
	iface := flags.String("interface", "", "Ethernet interface for -goose ethernet")
	controls := flags.Bool("controls", true, "allow modeled direct binary controls in the simulator")
	stationMode := flags.Bool("station", false, "start all server IEDs on a shared software bus, with engineered GOOSE/SV inputs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	m, doc, err := loadWorkspace(*file)
	if err != nil {
		return err
	}
	defer m.Close()
	if *stationMode {
		if *file == "" {
			doc, err = m.Import("station.scd", sample.StationSCL())
			if err != nil {
				return err
			}
		}
		station, err := m.StartStationWithControls(doc.ID, *controls)
		if err != nil {
			return err
		}
		for _, device := range station.Devices {
			fmt.Printf("%s/%s · MMS %s\n", device.IED, device.AP, device.Address)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = m.WaitStation(ctx, doc.ID)
		if stopErr := m.StopStation(doc.ID); stopErr != nil {
			return stopErr
		}
		if err == context.Canceled {
			return nil
		}
		return err
	}
	if *file == "" {
		doc, err = m.Import("bay.scd", workbench.SampleSCL())
		if err != nil {
			return err
		}
	}
	d, err := m.Select(doc.ID, *ied, *ap)
	if err != nil {
		return err
	}
	d, err = m.Start(d.ID, workbench.SimulationOptions{Listen: *address, GOOSE: *gooseMode, Interface: *iface, Controls: *controls})
	if err != nil {
		return err
	}
	fmt.Printf("Simulating %s/%s · MMS %s · GOOSE %s\n", *ied, *ap, d.Address, d.GOOSE)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = m.Wait(ctx, d.ID)
	if stopErr := m.Stop(d.ID); stopErr != nil {
		return stopErr
	}
	if err == context.Canceled {
		return nil
	}
	return err
}
