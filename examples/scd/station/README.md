# One SCD, three IED roles

[中文](README.zh-CN.md)

```sh
go run ./examples/scd/station
```

Run from the repository root. The example loads `station.scd`, opens a shared
software Ethernet bus and starts MUA and BayIED MMS runtimes. StationHMI is the
monitoring role. Each runtime and receiver is cancelled and joined on exit.

| Path | What to replace for equipment integration |
| --- | --- |
| MUA SV source | `Sample` fills the model-defined current/voltage fields for every sample. Replace its sine-wave generator with acquisition data. |
| BayIED SMV inputs | `System.SVInput` resolves I1..I3 and U1..U3 ExtRefs to actual source fields. Keep processing continuous samples and their qualities in your measurement driver. |
| MMXU1.TotW | The example averages instantaneous three-phase power over 80 samples, using this fixture's 1 mA / 10 mV scale. Commit the result with its q and t through `Signal.Sample`. |
| XCBR1.Pos | `OnOperate` is the virtual actuator. Replace it with the equipment driver and separately acquire position feedback. |
| StationHMI | The MMS client subscribes to events01 and the GOOSE receiver resolves breakerPosition through the SCD ExtRef. Replace stdout with the HMI/event store. |

Expected power is approximately 6900 W. Integer sample quantization produces
a small difference. The example explicitly accepts SampleSync=0 from its
software clock. Go timers provide a demonstration cadence. Physical sampling,
synchronization and protection performance require the equipment clock and
commissioning tests.

The SCD structure was validated against the IEC 61850-6 2007B4 XSD.
[Engineering API and buffered reporting](../../../docs/api/engineering.md)
