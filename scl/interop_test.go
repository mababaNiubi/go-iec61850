// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"context"
	"github.com/mababaNiubi/go-iec61850/mms"
	"github.com/mababaNiubi/go-iec61850/scl"
)

func TestIndependentCArrayAlternateAccess(t *testing.T) {
	peer, root := os.Getenv("LIBIEC61850_ARRAY_CLIENT"), os.Getenv("LIBIEC61850_SCL_EXAMPLES")
	if peer == "" || root == "" {
		t.Skip("set LIBIEC61850_ARRAY_CLIENT and LIBIEC61850_SCL_EXAMPLES")
	}
	system, err := scl.ParseFile(filepath.Join(root, "server_example_complex_array", "mhai_array.cid"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := system.Device("test", "accessPoint1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, r, _ := serviceRuntime(t, d)
	host, port, _ := net.SplitHostPort(r.Address())
	output, err := exec.CommandContext(ctx, peer, host, port).CombinedOutput()
	if err != nil {
		t.Fatalf("independent C array client: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// The executable is built from an independent C implementation, kept in
// _local with reference sources. Normal SDK tests do not need a C compiler.
func TestIndependentCClientModeledServices(t *testing.T) {
	peer := os.Getenv("LIBIEC61850_MODEL_CLIENT")
	if peer == "" {
		t.Skip("set LIBIEC61850_MODEL_CLIENT to the independent C peer")
	}
	for mode := 1; mode <= 4; mode++ {
		t.Run(strconv.Itoa(mode), func(t *testing.T) {
			x := fixture(t)
			modes := []string{"", "direct-with-normal-security", "sbo-with-normal-security", "direct-with-enhanced-security", "sbo-with-enhanced-security"}
			x = strings.Replace(x, "<Val>direct-with-normal-security</Val>", "<Val>"+modes[mode]+"</Val>", 1)
			x = strings.Replace(x, `<DA name="Oper"`, `<DA name="SBO" bType="VisString129" fc="CO"/><DA name="SBOw" bType="Struct" type="Operate" fc="CO"/><DA name="Cancel" bType="Struct" type="Operate" fc="CO"/><DA name="Oper"`, 1)
			d := device(t, x)
			pos := lookup(t, d, "XCBR1", "ST", "Pos.stVal")
			if err := d.OnControl(lookup(t, d, "XCBR1", "CO", "Pos.Oper"), func(ctx context.Context, c scl.Command) error {
				state := scl.Open
				if c.Value {
					state = scl.Closed
				}
				v, _ := state.MMSValue()
				return d.Update(ctx, mms.Assignment{Name: pos, Value: v})
			}); err != nil {
				t.Fatal(err)
			}
			ctx, r, _ := serviceRuntime(t, d)
			host, port, _ := net.SplitHostPort(r.Address())
			cmd := exec.CommandContext(ctx, peer, host, port, strconv.Itoa(mode))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(output)
			for scanner.Scan() {
				if scanner.Text() == "OFFLINE" {
					for _, state := range []scl.Position{scl.Open, scl.Closed} {
						v, _ := state.MMSValue()
						if err := d.Update(ctx, mms.Assignment{Name: pos, Value: v}); err != nil {
							t.Fatal(err)
						}
					}
					input.Write([]byte("Y\n"))
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("C client: %v\n%s", err, stderr.String())
			}
		})
	}
}
