# Values and types: convert between Go and MMS

English | [简体中文](data.zh-CN.md)

`mms.Value` and `mms.TypeSpec` are aliases of the types in `data`. MMS applications can import only `mms`; GOOSE datasets typically use `data`. A `Value` is an immutable snapshot. Constructors and accessors such as `Bytes` and `Elements` copy mutable containers.

The snippets use the standard `context` and `fmt` packages and `github.com/mababaNiubi/go-iec61850/mms`. The UTC constructor below also needs `time`.

## Construct and access a value

| Wire value | Construct for writing | Access after reading | Go result |
|---|---|---|---|
| Boolean | `mms.Boolean(true)` | `value.Bool()` | `bool, error` |
| Signed integer | `mms.Integer(99)` | `value.Int()` | `int64, error` |
| Unsigned integer | `mms.Unsigned(99)` | `value.Uint()` | `uint64, error` |
| Float32 / Float64 | `mms.Float32(1.25)` / `mms.Float64(1.25)` | `value.Float()` | `float64, error` |
| Visible ASCII string | `mms.VisibleString("hello")` | `value.StringValue()` | `string, error` |
| UTF-8 MMSString | `mms.MMSString("测量值")` | `value.StringValue()` | `string, error` |
| OctetString | `mms.OctetString([]byte{1, 2})` | `value.Bytes()` | `[]byte, error` |
| UTC | `mms.UTC(time.Now())` | `value.Time()` / `value.Bytes()` | Go time / raw bytes retaining quality |
| Array / Structure | `mms.Array(...)` / `mms.Structure(...)` | `value.Elements()` | `[]Value, error` |

Use `value.String()` for display and `StringValue()` to access a string value. Accessors do not coerce types: `Int()` does not convert an unsigned integer or float. Always handle the returned error. `Float()` interprets the original Float32/64 representation as a Go floating-point value; use `Bytes()` for the exact wire representation. Floating-point bytes include an exponent-width prefix, so they are not a bare 4/8-byte IEEE payload.

```go
func PrintValue(value mms.Value) error {
    switch value.Kind() {
    case mms.KindInteger:
        n, err := value.Int()
        if err != nil { return err }
        fmt.Printf("integer=%d\n", n)
    case mms.KindBoolean:
        b, err := value.Bool()
        if err != nil { return err }
        fmt.Printf("boolean=%t\n", b)
    default:
        fmt.Printf("type=%s value=%s\n", value.Kind(), value)
    }
    return nil
}
```

## Read arrays and structures

```go
func ReadArray(ctx context.Context, client *mms.Client) error {
    value, err := client.Read(ctx, mms.DomainName("sample", "array"))
    if err != nil { return err }
    elements, err := value.Elements()
    if err != nil { return err }
    for i, element := range elements {
        number, err := element.Int()
        if err != nil { return fmt.Errorf("element %d: %w", i, err) }
        fmt.Printf("array[%d]=%d\n", i, number)
    }
    return nil
}
```

A structure also stores values in order; member names belong to `TypeSpec.Components`. If the member name is known and the peer supports it, use `mms.Member` for direct access.

## Declare a server variable type

```go
func MakeModel() (*mms.Model, error) {
    model := mms.NewModel()
    integer := mms.TypeSpec{Kind: mms.KindInteger, Width: 32}
    err := model.Add(mms.Variable{
        Name: mms.DomainName("sample", "array"),
        Type: mms.TypeSpec{Kind: mms.KindArray, Length: 3, Element: &integer},
        Value: mms.Array(mms.Integer(1), mms.Integer(2), mms.Integer(3)),
        Writable: true,
    })
    return model, err
}
```

- `Width` is measured in bits. For arrays, `Length` is the element count.
- For strings and bit strings, positive `Length` means a fixed length, negative means a maximum, and zero permits only an empty value. MMSString length counts Unicode characters.
- Declare Float32 as `Width: 32, ExponentWidth: 8`; Float64 uses 64 and 11.
- `TypeOf` infers integer width as 64 bits and leaves structure members unnamed. Write an explicit `TypeSpec` when publishing a 32-bit integer or named members.

## UTC, bit strings, and raw bytes

UTC bytes retain the fraction and quality byte. `Time()` converts them to Go time with the corresponding precision conversion. Use `UTCFromBytes([8]byte{...})` to retain known quality bits. Calling `UTC(time)` does not establish clock synchronization.

BitString follows ASN.1 bit order: the most significant bit of the first byte is the first bit. Supply the significant bit count and clear unused trailing bits:

```go
func MakeBits() (mms.Value, error) {
    // Three significant bits, 101; the remaining five bits must be zero.
    return mms.BitString([]byte{0xa0}, 3)
}
```

SV `seqData` uses an application-defined byte layout without MMS type tags. See [custom SV layouts](sv.md#define-a-custom-sample-layout).
