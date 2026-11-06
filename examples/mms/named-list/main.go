// SPDX-License-Identifier: GPL-3.0-or-later
// 使用命名变量表把多个变量作为一组读写，并显式删除列表。
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

	// 这里建立 AA 命名变量表：只属于当前关联，断开后自动消失。
	// 这不是 IEC 61850 SCL 数据集配置；对端必须支持动态定义 MMS 列表。
	name := mms.AssociationName("watch")
	variables := []mms.VariableAccess{
		mms.Access(mms.DomainName("sample", "int")),
		mms.Access(mms.DomainName("sample", "bool")),
	}
	if err := client.DefineNamedList(ctx, name, variables); err != nil {
		return err
	}

	// 表内顺序即数值顺序；写入时数量和类型必须逐项对应。
	failures, err := client.WriteNamedList(ctx, name, []mms.Value{mms.Integer(100), mms.Boolean(false)})
	if err != nil {
		return err
	}
	for i, failure := range failures {
		if failure != nil {
			return fmt.Errorf("写列表第 %d 项: %w", i, failure)
		}
	}
	results, err := client.ReadNamedList(ctx, name)
	if err != nil {
		return err
	}
	for i, result := range results {
		if result.Error != nil {
			return fmt.Errorf("读列表第 %d 项: %w", i, result.Error)
		}
		fmt.Printf("watch[%d] = %s\n", i, result.Value)
	}

	// 显式删除后再 Conclude；不能在关联已经释放后发送删除请求。
	deleted, err := client.DeleteNamedList(ctx, name)
	if err != nil {
		return err
	}
	fmt.Printf("deleted lists: %d\n", deleted.Deleted)

	// 等本场景请求结束后正常释放关联；不要与尚未结束的读写并发调用。
	return client.Conclude(ctx)
}
