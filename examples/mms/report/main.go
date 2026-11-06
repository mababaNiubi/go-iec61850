// SPDX-License-Identifier: GPL-3.0-or-later
// 发送并接收一条原始 MMS InformationReport；本例包含本机两端。
package main

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	associated := make(chan *mms.Association, 1)

	// 服务端：每个连接有自己的 Association，报告通过该对象发给对应客户端。
	address, stop, err := mmsdemo.Start("", func(a *mms.Association) {
		select {
		case associated <- a:
		case <-a.Context().Done():
		}
	})
	if err != nil {
		return err
	}
	defer stop()

	// 客户端：Dial 后 Reports 已经可以接收非确认报告，不需要轮询 Read。
	client, err := mms.Dial(ctx, address, mms.Config{})
	if err != nil {
		return err
	}
	defer client.Close()
	var association *mms.Association
	select {
	case association = <-associated:
	case <-ctx.Done():
		return ctx.Err()
	}

	// 服务端业务在测量值变化时组织快照。Variables 和 Results 必须一一对应。
	report := mms.InformationReport{
		Variables: []mms.VariableAccess{mms.Access(mms.DomainName("sample", "int"))},
		Results:   []mms.ReadResult{{Value: mms.Integer(123)}},
	}
	if err := association.SendReport(ctx, report); err != nil {
		return err
	}

	// 客户端业务：既要检查通道是否关闭，也要检查报告中每一项的 Error。
	select {
	case received, open := <-client.Reports():
		if !open {
			return client.Err()
		}
		for i, result := range received.Results {
			if result.Error != nil {
				return result.Error
			}
			fmt.Printf("report %s = %s\n", received.Variables[i].Name, result.Value)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	// Reports 通道由库管理；应用不要 close(client.Reports())。
	return client.Conclude(ctx)
}
