# Read SV fields from an SCL model

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```sh
go run ./examples/sv/scl
```

The public SCL parser resolves `MUA/AP1/LD0/MSVCB01`: a 64-byte 4I4U dataset with two ASDUs per frame. The example finds the current and its Quality by name, encodes values without hard-coded offsets, transfers a complete Ethernet frame through `simulator.MemoryLink`, and receives both samples with `sv.Receiver`.

Expected current values are 12345 and 12346 at smpCnt 0 and 1. These are raw instantaneous values; engineering scale belongs to the application. Software samples explicitly use smpSynch=0 and opt in to `AnySynchronization`.

To receive actual device traffic, create an `ethernet.Conn` with `sv.EtherType`, use the profile's normal synchronization policy and pass that link to Receiver.Run. Register additional subscribers on the same receiver rather than starting competing readers on one NIC. [SCL/SV and lifetime API](../../../docs/api/ied.md#import-an-sv-model-and-share-one-reader)
