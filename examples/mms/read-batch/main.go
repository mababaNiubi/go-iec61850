// SPDX-License-Identifier: GPL-3.0-or-later
// 批量读取并处理逐项失败，再读取数组元素和结构成员。
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

	accesses := []mms.VariableAccess{
		mms.Access(mms.DomainName("sample", "int")),
		mms.Access(mms.DomainName("sample", "bool")),
		mms.Access(mms.DomainName("sample", "missing")), // 故意请求不存在的变量。
	}
	results, err := client.ReadMultiple(ctx, accesses...)
	if err != nil {
		return err
	} // 这是整个请求的错误，如断连或协议错误。
	for i, result := range results {
		// 每一项还可能失败；不能因为上面的 err==nil 就读取所有 Value。
		if result.Error != nil {
			fmt.Printf("%s: %v\n", accesses[i].Name, result.Error)
			continue
		}
		fmt.Printf("%s = %s (type=%s)\n", accesses[i].Name, result.Value, result.Value.Kind())
	}

	// 单层 AlternateAccess：数组下标从 0 开始，结构通过成员名选择。
	item, err := client.ReadAccess(ctx, mms.Element(mms.DomainName("sample", "array"), 1))
	if err != nil {
		return err
	}
	second, err := item.Int()
	if err != nil {
		return err
	}
	member, err := client.ReadAccess(ctx, mms.Member(mms.DomainName("sample", "struct"), "count"))
	if err != nil {
		return err
	}
	count, err := member.Int()
	if err != nil {
		return err
	}
	fmt.Printf("array[1]=%d, struct.count=%d\n", second, count)

	// 等本场景请求结束后正常释放关联；不要与尚未结束的读写并发调用。
	return client.Conclude(ctx)
}
