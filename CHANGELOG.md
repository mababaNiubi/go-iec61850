# Changelog

## Unreleased

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
