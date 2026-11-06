# Development, builds, and validation

English | [简体中文](development.zh-CN.md)

Run commands from the repository root. Go 1.24 or later is required. The runtime library has no third-party Go modules and ordinary builds do not need cgo.

## Routine checks

```bash
go test ./... -count=1 -timeout=120s
go test -race ./... -count=1 -timeout=120s
go vet ./...
```

The race detector requires a supported OS/architecture and a local C compiler. Production builds are a separate configuration: the scripts explicitly set `CGO_ENABLED=0`.

```powershell
./scripts/test.ps1 -Race -Examples
./scripts/build.ps1
./scripts/build.ps1 -TargetOS linux -TargetArch amd64
```

```bash
bash scripts/test.sh --race --examples
bash scripts/build.sh
GOOS=linux GOARCH=amd64 bash scripts/build.sh
```

Test scripts save JSON output under `_local/reports/unit/`. `-Examples` / `--examples` also runs the eight API examples, which can be run individually. Build scripts use `CGO_ENABLED=0` and `-trimpath` and place the six tools under `_local/bin/<os>-<arch>/`, with `.exe` on Windows. Cross-compilation establishes build compatibility; running on the target OS is a separate check.

When fixing a protocol defect, add a focused regression that reproduces it. Cover the relevant error type, preservation of valid state, deadlines/cancellation, and resource cleanup. For example, fuzz BER parsing with:

```bash
go test ./internal/ber -run '^$' -fuzz FuzzParse -fuzztime 10s
```

Unit tests do not automatically choose a network interface. Explicit interface environment variables enable platform access or live-frame checks; use a dedicated test interface.

## Optional local interoperability material

The entire `_local/` directory is ignored by Git:

| Path under `_local/` | Contents |
|---|---|
| `interop/reference` | Pinned libiec61850 source, revision metadata, and exported SHA-256 manifest |
| `interop/tests` | C fixtures, C HAL, and Go/C interoperability tests |
| `interop/scripts` | C baseline, offline, and explicit-interface validation scripts |
| `standards` | Standards, source index, and user-provided reference material |
| `reports` | Logs, JSON summaries, PCAP files, and historical evidence |
| `bin` | Current build output |
| `archive` | Earlier documents, builds, and initialization license material |

These files are not distributed with the Git repository. Ordinary builds and Go unit tests do not depend on them. If the local reference material is present, use its separate module:

```powershell
cd _local/interop
./scripts/test.ps1 -BuildReference -Race
```

```bash
cd _local/interop
bash scripts/test.sh --build-reference --race
```

The module uses `replace github.com/mababaNiubi/go-iec61850 => ../..` to exercise the working library. C builds require gcc/ar. Reference version v1.6.2.1 is pinned; source verification failures stop the scripts. Live-interface scripts require an explicit interface, with optional peer interfaces for GOOSE/SV.

## Git contents

Commit production Go code, package unit tests, fuzz regression inputs, examples, general documentation, build scripts, and license files. `.gitignore` excludes `_local/`, IDE settings, environment files, C sources, standards PDFs, captures, logs, caches, and binaries.

```bash
git status --short --untracked-files=all
git check-ignore _local/standards _local/interop/reference .idea
```

Use repository-relative paths, loopback addresses, and interface placeholders in published guides and examples. Keep host details, credentials, actual Npcap identifiers, and machine-specific absolute paths in ignored local material. Standards retain their publishers' copyright. A source index does not establish that every full standard has been checked or that certification has been achieved.

[Architecture](architecture.md) · [Platform support](platforms.md)
