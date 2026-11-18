# Changelog

## Unreleased

- Add a TLS transport under the unchanged ISO/ACSE/MMS stack with strict policy validation, mutual certificate authentication, redacted security events and no silent downgrade.
- Add ACSE password authentication on both roles, with credentials never silently ignored, connection state/lost callbacks, TCP keepalive and a server connection limit.
- Support time-activated control through operTm on the seven-field Oper layout, explicit immediacy for a zero timestamp, and a remote read/write access policy.
- Model GoCB online management (GoEna gating, mapped attributes) and read/write client APIs for GoCB/SVCB; extend the TLS policy with CRLs, certificate pinning and peer-certificate access.
- Model sampled-value control blocks with SvEna gating and BRCB ResvTms/Owner reservation; these follow the pinned reference implementation and are marked implementation-derived (OptFlds bit assignment is not verified against normative text).
- Add read-only MMS file services (directory/open/read/close) with bounded retrieval, a virtual server-side provider and COMTRADE import from a remote IED in the workbench; verified against the pinned C reference client.
- Declare the reviewed client/server/publisher/subscriber service profile and pin the negotiated MMS advertisement to it.
- Verify report generations under configuration changes: typed `ErrConfigurationChanged`, verified one-shot snapshots and subscriber termination instead of decoding under a stale declaration.
- Add reusable dataset bindings with metadata reuse, generation refresh and fail-fast stale reads; bound discovery with request, byte and object budgets that stop deterministically before further work.
- Add a typed discovery tree and versioned selection documents that revalidate point by point after reconnect.
- Bind dynamically created domain datasets to report control blocks through an explicit ownership/re-read workflow, and mirror them into the modeled server's dataset registry.
- Extend the workbench with an acquisition panel (bindings, dynamic datasets, RCB rebind, selection revalidation) and add a runnable feeder acquisition example.

## 1.4.1 - 2026-11-17

- Animate ST/MX process values with random, ramp, sine and sequence generators that publish through reports, GOOSE and SV while staying independent of the simulator lifecycle.
- Import COMTRADE 1991/1999/2013 recordings (ASCII, BINARY, BINARY32, FLOAT32) and replay channels onto model points with decimation and looping, mapping digital channels to BOOL/INT/Dbpos.
- Record structured control history with originator and ctlNum evidence, add `GET /api/controls` and render a filterable Controls table with JSON export.
- Aggregate MMS reads into one journal line per second so polling clients cannot evict control records.
- Document generators, recorded playback, control history and the new HTTP endpoints in English and Simplified Chinese.

## 1.4.0 - 2026-11-12

- Serve coherent multi-member MMS reads through an optional batch store capability, preserving existing Read decorators.
- Retain FCDA alternate access in GOOSE layouts and input/quality resolution.
- Inspect SubNetworks, ServerAt ports, scoped inputs, report capabilities and primary-equipment terminals; compare model revisions.
- Isolate station buses by SubNetwork and expose source quality, receive expiry and complete ASDU/GOOSE application callbacks.
- Discover online IED services without SCL and read typed current dataset snapshots independently of buffered history.
- Add IEDLab online report, control and settings panels, structured session history and engineering-network comparison.
- Refactor the station example to consume the public station callbacks and validate its process-bus connection.
- Reorganize English/Chinese API workflows, correct stale capability descriptions, document lifecycle/default/error contracts and add executable GoDoc examples.

## 1.3.0 - 2026-11-07

- Inspect primary equipment, per-DO diagnostics, typed points and dataset order through the SCL catalog.
- Serve engineered URCB/BRCB reports, buffering, GI, integrity, segmentation and explicit history resumption.
- Add modeled binary control selection/cancellation/termination, ordinary SP callbacks, and SG/SE editing and activation.
- Connect multiple simulated IEDs through a shared software Ethernet bus and resolve SMV ExtRef inputs.
- Extend IEDLab with engineering catalogs, online model checks and actual report subscriptions. Add station and settings examples.
- Fix change reporting after SP writes and active SG changes through one coherent publication boundary.
- Acknowledge enhanced controls before execution and correlated positive/negative command termination.
- Accept UTF-8 BOM, unnamed enum ordinals, service-tracking basic types, and spaced ISO selectors/AP titles.
- Model bounded DO/SDO/DA/BDA arrays, indexed instance values, and nested FCDA alternate access.
- Add public station orchestration and modeled MU sources, plus shared GOOSE/SV inputs in the browser and CLI.
- Verify the reference SCL corpus and independent C nested-array directory/read/dataset access.

## 1.2.0 - 2026-11-07

- Add public `ied` clients for all four immediate control models, correlated enhanced command termination, and bounded URCB subscriptions with one unsolicited-message dispatcher.
- Add public `simulator` runtime and in-memory frame links; IEDLab now composes these public APIs.
- Resolve multicast SV profiles from the shared SCL parser, preserving original basic types, FCDA/declaration order, checked layouts, and engineered metadata.
- Add shared GOOSE/SV receivers, structured component access, explicit IEC Quality flags, and annotated integration examples.
- Verify the extracted APIs through the edge-ai gateway against independent libIEC61850 controls, URCB, GOOSE and SV peers.

- Consolidate diagnostics into the `iedlab` CLI with a browser workbench, SCL import, model browsing, typed value editing, MMS/GOOSE simulation, remote MMS client and live event journal.
- Share the sample SCL between the application and examples, and document debugging and driver customization in English and Simplified Chinese.

## 1.1.0 - 2026-11-07

- Resolve SCD access points, model paths, static datasets and Layer 2 GOOSE streams using the new `scl` package and `scdinspect` tool.
- Connect acquisition, direct binary control, actual feedback and ExtRef-based monitoring with the [breaker IED example](examples/scd/breaker/README.md).
- Bind value, quality and timestamp paths with `Signal`, and commit coherent acquisitions using `IED.Update` or `Model.SetValues`.
- Discover GOOSE inputs on server and client-only access points, and load configured MMS addresses and OSI selectors.
- Add English and Simplified Chinese integration guides, common path tables, and separate device/monitor launch modes.

## 1.0.0 - 2026-11-07

Initial tagged release of the native Go implementation.

- MMS client and server over TCP/ISO, including typed reads/writes, batch and alternate access, browsing, named variable lists, raw InformationReport, and an extensible Store.
- Layer 2 GOOSE codecs, stateful publication/retransmission, subscriptions, and TAL expiry/recovery.
- Layer 2 Sampled Values with multiple ASDUs, sample counters, gap/timeout handling, and explicit payload-layout helpers.
- Optional raw Ethernet adapters for Windows amd64 through Npcap and Linux through AF_PACKET. Production runtime code requires no cgo or third-party Go modules.
- Eight focused application examples, six command-line tools, and English-default API, example, platform, architecture, and development guides with Simplified Chinese counterparts.
- C references, private interoperability fixtures, standards, captures, reports, credentials, and generated artifacts remain outside Git through ignored local directories.

### Scope

Full ACSI/SCL modeling, report control blocks, R-GOOSE/R-SV, PTP, hardware acquisition, and security extensions are not implemented. VLAN delivery and software timing depend on the host network path; see [platform support](docs/platforms.md). This release does not claim IEC conformance certification.

Licensed under GPL-3.0-or-later; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
