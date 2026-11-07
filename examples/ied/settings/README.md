# SP parameters and setting groups

[中文](README.zh-CN.md)

```sh
go run ./examples/ied/settings
```

The SCD contains PTOC1.OpDlTmms as an ordinary SP delay and PTOC1.StrVal as
an ASG setting with two groups. A local MMS server applies SP range validation
through OnSetpoint. The client commits group 2's threshold to 25 while the
active group 1 value remains 10, then explicitly activates group 2.

Replace the SP callback with your configuration driver. Read the active SG
view in the equipment application when applying its selected parameter set.
The example retains parameters in the model process. Persist equipment
configuration through your application's storage lifecycle when required.

The SCD structure was validated against the 2007B4 XSD.
[API guide](../../../docs/api/engineering.md#control-and-configure-equipment)
