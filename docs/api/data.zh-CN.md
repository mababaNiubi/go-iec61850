# Value 和 TypeSpec：Go值与MMS值怎么转换

[English](data.md) | 简体中文

`mms.Value`和`mms.TypeSpec`是`data`中类型的别名。MMS业务可以只导入mms；GOOSE数据项通常使用data。同一个Value是不可变快照，构造函数和Bytes/Elements等访问器会复制可变容器。

代码使用标准库context、fmt和库包`github.com/mababaNiubi/go-iec61850/mms`。

## 构造值、读取值

| 对端数据 | 写入时构造 | 读取后转换 | 返回的Go类型 |
|---|---|---|---|
| Boolean | `mms.Boolean(true)` | `value.Bool()` | `bool,error` |
| 有符号整数 | `mms.Integer(99)` | `value.Int()` | `int64,error` |
| 无符号整数 | `mms.Unsigned(99)` | `value.Uint()` | `uint64,error` |
| Float32 / Float64 | `mms.Float32(1.25)` / `mms.Float64(1.25)` | `value.Float()` | `float64,error` |
| 可见ASCII字符串 | `mms.VisibleString("hello")` | `value.StringValue()` | `string,error` |
| UTF-8 MMSString | `mms.MMSString("测量值")` | `value.StringValue()` | `string,error` |
| OctetString | `mms.OctetString([]byte{1,2})` | `value.Bytes()` | `[]byte,error` |
| UTC | `mms.UTC(time.Now())` | `value.Time()` / `value.Bytes()` | 时间或保留质量的原始字节 |
| Array / Structure | `mms.Array(...)` / `mms.Structure(...)` | `value.Elements()` | `[]Value,error` |

`value.String()`供打印，`StringValue()`才是字符串类型访问器。`Int()`不会把Unsigned或Float自动转成有符号整数；类型不匹配时返回错误，不能忽略它。Float访问保留Float32/64原来的位语义，返回Go浮点值；需要精确原始表示时用Bytes；Float的原始Bytes包含首字节指数宽度，不能直接当作4/8字节IEEE数值。

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

## 解析数组或结构

```go
func ReadArray(ctx context.Context, client *mms.Client) error {
    value, err := client.Read(ctx, mms.DomainName("sample", "array"))
    if err != nil { return err }
    elements, err := value.Elements()
    if err != nil { return err }
    for i, element := range elements {
        number, err := element.Int()
        if err != nil { return fmt.Errorf("第%d个元素: %w", i, err) }
        fmt.Printf("array[%d]=%d\n", i, number)
    }
    return nil
}
```

结构Value同样按顺序保存子值，成员名在TypeSpec.Components中。已知成员名时可直接用`mms.Member`读取单项；不必下载整个结构再索引。

## 为自己的服务声明类型

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

- `Width`是位数；`Length`对数组是元素数量。
- 字符串/位串的正Length表示固定长度，负值表示最大长度，0只允许空值。MMSString按Unicode字符计数。
- Float32/64声明分别为Width32/64、ExponentWidth8/11。
- `TypeOf`是推断工具：整数推断为64位、结构成员不带名字。需要对外声明32位或有名字的成员时，自己写TypeSpec。

## UTC、位串和原始字节

UTC的Bytes包含小数和质量字节；`Time()`是便于业务使用的Go时间，会有精度转换。已知质量时使用`UTCFromBytes([8]byte{...})`保留原位，`UTC(time)`本身不建立时间同步。

BitString使用ASN.1位序：首字节最高位是第一位。构造时给出有效位数，末尾未使用位必须为0：

```go
func MakeBits() (mms.Value, error) {
    // 101 共3位，最后5位必须清零。
    return mms.BitString([]byte{0xa0}, 3)
}
```

SV的seqData使用原始采样布局，不使用这些MMS类型标签。见 [SV布局API](sv.zh-CN.md#自己定义采样值布局)。
