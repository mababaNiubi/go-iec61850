# Define an SV sample layout

English | [简体中文](README.zh-CN.md)

Run from the repository root:

```bash
go run ./examples/sv/data-layout
```

Start with the offset table in `main.go`, then follow the setters and matching readers. Numeric fields use network byte order. The layout does not depend on Go struct alignment, and accessor errors are handled.

The example also preserves floating-point negative zero and raw UTC bytes. See [custom sample layouts](../../../docs/api/sv.md#define-a-custom-sample-layout).

[Annotated source](main.go). Expected key output:

```text
value=-123 quality=0x400 float=1.25 negativeZero=true utc=6553f1008000000a
```
