// SPDX-License-Identifier: GPL-3.0-or-later
package scl_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mababaNiubi/go-iec61850/scl"
)

// Point this at an unmodified libIEC61850 v1.6.2.1 examples directory. The
// optional upstream corpus is not copied into the Go module or its releases.
func TestReferenceSCLProfiles(t *testing.T) {
	root := os.Getenv("LIBIEC61850_SCL_EXAMPLES")
	if root == "" {
		t.Skip("set LIBIEC61850_SCL_EXAMPLES to the independent reference corpus")
	}
	passed, knownInvalid := 0, 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := strings.ToLower(filepath.Ext(path))
		if entry.IsDir() || (ext != ".cid" && ext != ".icd" && ext != ".scd") {
			return nil
		}
		system, err := scl.ParseFile(path)
		if err == nil {
			for _, ap := range system.AccessPoints() {
				if ap.HasServer {
					_, err = system.Device(ap.IED, ap.AP)
					if err != nil {
						break
					}
				}
			}
		}
		parent := filepath.Base(filepath.Dir(path))
		if parent == "server_example_setting_groups" || parent == "server_example_simple" || parent == "server_example_write_handler" {
			if err == nil {
				t.Errorf("invalid upstream declaration was silently accepted: %s", path)
			} else {
				knownInvalid++
				t.Logf("rejected upstream model (%s): %v", parent, err)
			}
			return nil
		}
		if err != nil {
			t.Errorf("reference model %s: %v", path, err)
		} else {
			passed++
			t.Logf("loaded %s", parent)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if passed == 0 {
		t.Fatal("no reference models were tested")
	}
	t.Logf("created %d reference device models; explicitly rejected %d invalid upstream declarations", passed, knownInvalid)
}
