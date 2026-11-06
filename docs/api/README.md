# API guide

English | [简体中文](README.zh-CN.md)

Choose the task you are implementing. Each guide explains the objects, parameters, results, and a runnable example.

| Task | API documentation | Runnable source |
|---|---|---|
| Connect and read or write a variable | [MMS: connect and read/write](mms.md#connect-and-read-or-write-an-integer) | [read-write](../../examples/mms/read-write/main.go) |
| Read several variables and handle partial failures | [MMS: batch reads](mms.md#read-several-variables) | [read-batch](../../examples/mms/read-batch/main.go) |
| Discover device names and declared types | [MMS: browse](mms.md#discover-domains-variable-names-and-types) | [browse](../../examples/mms/browse/main.go) |
| Access an array element or structure member | [MMS: partial access](mms.md#read-an-array-element-or-structure-member) | [read-batch](../../examples/mms/read-batch/main.go) |
| Group variables into a named list | [MMS: named lists](mms.md#read-and-write-a-named-variable-list) | [named-list](../../examples/mms/named-list/main.go) |
| Receive a server-initiated report | [MMS: InformationReport](mms.md#receive-and-send-informationreport) | [report](../../examples/mms/report/main.go) |
| Create an MMS server | [MMS: server](mms.md#create-an-mms-server) | [local peer](../../examples/internal/mmsdemo/server.go) |
| Convert an MMS Value to a Go value | [Values and types](data.md) | [read-write](../../examples/mms/read-write/main.go) |
| Publish and subscribe to state changes | [GOOSE API](goose.md) | [publish-subscribe](../../examples/goose/publish-subscribe/main.go) |
| Publish, receive, or decode sampled values | [SV API](sv.md) | [publish-subscribe](../../examples/sv/publish-subscribe/main.go) |
| Define the SV payload byte layout | [SV: custom layout](sv.md#define-a-custom-sample-layout) | [data-layout](../../examples/sv/data-layout/main.go) |

Package paths start with `github.com/mababaNiubi/go-iec61850/`: import `mms`, `data`, `goose`, `sv`, or `ethernet` as needed. MMS connects to a TCP address. GOOSE and SV require an Ethernet interface or an application implementation of `FrameIO`.

[Running the examples](../../examples/README.md) · [Command-line tools](../../cmd/README.md)
