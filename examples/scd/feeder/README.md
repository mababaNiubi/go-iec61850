# Feeder acquisition workflow (EX-01)

One runnable program covering the complete station-SCADA acquisition flow over
public packages only. The modeled bay (XCBR position/control, MMXU
measurements, one BRCB) is served over loopback TCP; the client exercises:

- **Verified binding** — `BindDataSet` with per-read declaration rechecks
  (`CFG-01`): a reconfigured peer is rejected, never decoded under a stale
  generation.
- **Buffered reporting** — `SubscribeBRCB` + GI; the report is appended to
  caller-owned storage and only then is the `EntryID` cursor saved.
- **Control with feedback** — `Operate` crosses real MMS; the application
  binds its own actuator through `scl.IED.OnControl` and returns independent
  position/quality/timestamp feedback via `Update`. A refused or uncertain
  outcome is surfaced, never retried automatically.
- **Dynamic dataset** — create, bind, reconfigure and observe the typed
  `ErrConfigurationChanged`, then `Refresh` to the next generation.
- **Reconnect** — a saved selection document is revalidated
  (`VerifySelection`) and the acknowledged cursor resumes the BRCB.

```sh
go run ./examples/scd/feeder
```

Replace the actuator callback and the storage slice with your equipment
driver and database. See [workbench guide](../../../docs/workbench.md) for
the browser workflow built on the same services.
