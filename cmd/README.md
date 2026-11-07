# IEDLab application

English | [简体中文](README.zh-CN.md)

The repository builds one executable, `iedlab`, with a browser workspace and terminal commands.

```sh
go run ./cmd/iedlab serve -http 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`, load an SCL file or the sample, then browse values and start an IED simulator. The frontend is embedded in Go. [Workbench guide](../docs/workbench.md)

| Command | Purpose |
|---|---|
| `iedlab serve [-file model.scd]` | Browser UI, HTTP API, simulator management and remote MMS client |
| `iedlab simulate -file model.scd -ied IED -ap AP` | Headless SCL-backed IED with MMS and GOOSE |
| `iedlab inspect -file model.scd` | Access points, paths, datasets, GOOSE and ExtRef inspection |
| `iedlab protocol <tool>` | Advanced MMS/GOOSE/SV diagnostics |

`protocol` accepts `mms-client`, `mms-server`, `goose-publisher`, `goose-subscriber`, `sv-publisher` and `sv-subscriber`. Existing options remain available with `-h`.

```sh
go run ./cmd/iedlab simulate -ied BayIED -ap S1 -listen 127.0.0.1:8102
go run ./cmd/iedlab inspect -file internal/sample/bay.scd -ied BayIED -ap S1 -match XCBR1.Pos
go run ./cmd/iedlab protocol sv-subscriber -list
```

`cmd/internal` contains diagnostic flag, capture and fixture helpers. Protocol libraries and focused [API examples](../examples/README.md) are separate from the application.
