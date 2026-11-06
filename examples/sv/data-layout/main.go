// SPDX-License-Identifier: GPL-3.0-or-later
// 按字节偏移组织采样值；不使用有对齐填充的 Go struct 内存布局。
package main

import (
	"fmt"
	"log"
	"math"

	"github.com/mababaNiubi/go-iec61850/sv"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// 应用约定布局（偏移/宽度）：INT32 0/4、Quality 4/4、Float32 8/4、Float64 12/8、UTC 20/8。
	// 所有数字均使用网络字节序；这里的 UTC 是保留质量字节的原始 8 字节。
	raw, err := sv.NewSampleData(28)
	if err != nil {
		return err
	}
	if err := raw.SetInt32(0, -123); err != nil {
		return err
	}
	if err := raw.SetQuality(4, 0x0400); err != nil {
		return err
	}
	if err := raw.SetFloat32(8, 1.25); err != nil {
		return err
	}
	if err := raw.SetFloat64(12, math.Copysign(0, -1)); err != nil {
		return err
	}
	if err := raw.SetUTCAt(20, [8]byte{0x65, 0x53, 0xf1, 0, 0x80, 0, 0, 0x0a}); err != nil {
		return err
	}

	measurement, err := raw.Int32(0)
	if err != nil {
		return err
	}
	quality, err := raw.Quality(4)
	if err != nil {
		return err
	}
	floating, err := raw.Float32(8)
	if err != nil {
		return err
	}
	negativeZero, err := raw.Float64(12)
	if err != nil {
		return err
	}
	utc, err := raw.UTCAt(20)
	if err != nil {
		return err
	}
	fmt.Printf("value=%d quality=%#x float=%g negativeZero=%t utc=%x\n", measurement, quality, floating, math.Signbit(negativeZero), utc)

	// Bytes 返回副本，交给 ASDU.Data。修改副本不会修改 raw 本身。
	payload := raw.Bytes()
	fmt.Printf("seqData (%d bytes)=%x\n", len(payload), payload)
	return nil
}
