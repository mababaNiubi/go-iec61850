// SPDX-License-Identifier: GPL-3.0-or-later
// iedlab is the single application entry point. Commands use the same Go
// model/runtime as the web API, so a breakpoint follows either execution path.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "iedlab:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		help()
		return nil
	}
	switch args[0] {
	case "serve":
		return serveApp(args[1:])
	case "simulate":
		return simulateApp(args[1:])
	case "inspect":
		return runInspect(args[1:])
	case "protocol":
		return protocol(args[1:])
	case "help", "-h", "--help":
		help()
		return nil
	default:
		return fmt.Errorf("unknown command %q; run iedlab help", args[0])
	}
}
func help() {
	fmt.Print(`IEC 61850 Workbench

Usage: iedlab <command> [options]

  serve       Start the browser workspace and HTTP API
  simulate    Load an SCL file and simulate an IED without the browser
  inspect     Inspect SCL access points, object paths and ExtRef inputs
  protocol    Advanced MMS/GOOSE/SV frame diagnostics

Examples:
  iedlab serve -http 127.0.0.1:8080
  iedlab serve -file internal/sample/bay.scd
  iedlab simulate -file internal/sample/bay.scd -ied BayIED -ap S1
  iedlab inspect -file internal/sample/bay.scd -ied StationHMI -ap S1 -inputs

Each command accepts -h. Session state is held in memory.
`)
}
func protocol(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("choose mms-client, mms-server, goose-publisher, goose-subscriber, sv-publisher or sv-subscriber")
	}
	switch args[0] {
	case "mms-client":
		return legacyMMSClient(args[1:])
	case "mms-server":
		return legacyMMSServer(args[1:])
	case "goose-publisher":
		return legacyGoosePublisher(args[1:])
	case "goose-subscriber":
		return legacyGooseSubscriber(args[1:])
	case "sv-publisher":
		return legacySVPublisher(args[1:])
	case "sv-subscriber":
		return legacySVSubscriber(args[1:])
	default:
		return fmt.Errorf("unknown protocol diagnostic %q", args[0])
	}
}
