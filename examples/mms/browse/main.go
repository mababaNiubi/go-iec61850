// SPDX-License-Identifier: GPL-3.0-or-later
// 发现设备目录和变量类型，避免预先猜测对象名。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/mababaNiubi/go-iec61850/examples/internal/mmsdemo"
	"github.com/mababaNiubi/go-iec61850/mms"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	address := flag.String("address", "", "MMS 设备地址；留空使用临时本机服务")
	flag.Parse()
	addressValue, stop, err := mmsdemo.Start(*address, nil)
	if err != nil {
		return err
	}
	defer stop()

	// 为整个场景设定时限；真实应用也可以分别为每次请求创建 context。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := mms.Dial(ctx, addressValue, mms.Config{})
	if err != nil {
		return err
	}
	defer client.Close() // 错误路径的兜底；正常路径在末尾 Conclude。

	// 先问设备有哪些 Domain，再询问某个 Domain 下的变量。
	domains, err := client.Names(ctx, mms.DomainObject, mms.VMDScope, "")
	if err != nil {
		return err
	}
	fmt.Println("domains:", domains)

	// Names 自动处理分页；只有需要控制每一页时才使用 GetNameList。
	names, err := client.Names(ctx, mms.NamedVariable, mms.DomainScope, "sample")
	if err != nil {
		return err
	}
	fmt.Println("sample variables:", names)

	// 类型说明是声明约束；不要通过一次读取到的数值猜整数宽度。
	name := mms.DomainName("sample", "int")
	spec, err := client.GetVariableAccessAttributes(ctx, name)
	if err != nil {
		return err
	}
	fmt.Printf("sample/int: type=%s width=%d\n", spec.Kind, spec.Width)

	// 等本场景请求结束后正常释放关联；不要与尚未结束的读写并发调用。
	return client.Conclude(ctx)
}
