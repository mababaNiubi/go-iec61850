# Changelog

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
