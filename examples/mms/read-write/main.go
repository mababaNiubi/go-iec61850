// SPDX-License-Identifier: GPL-3.0-or-later
// 读写一个整型变量：连接 → Read → Write → Read → Conclude。
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

	// 1. DomainName(domain,item) 是对端对象名，不是本机变量名。
	name := mms.DomainName("sample", "int")
	value, err := client.Read(ctx, name)
	if err != nil {
		return err
	}
	before, err := value.Int() // Int() 返回 int64；类型不符会返回错误。
	if err != nil {
		return err
	}

	// 2. MMS 写入使用带类型的 Value；这里写有符号整数，而不是字符串 "99"。
	if err := client.Write(ctx, name, mms.Integer(99)); err != nil {
		return err
	}

	// 3. 再读一次确认写入结果。真实设备可能有只读、联锁或异步更新策略。
	value, err = client.Read(ctx, name)
	if err != nil {
		return err
	}
	after, err := value.Int()
	if err != nil {
		return err
	}
	fmt.Printf("sample/int: %d -> %d\n", before, after)
	if after != 99 {
		return fmt.Errorf("写后读回得到 %d，期望 99", after)
	}

	// 等本场景请求结束后正常释放关联；不要与尚未结束的读写并发调用。
	return client.Conclude(ctx)
}
