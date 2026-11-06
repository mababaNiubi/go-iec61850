// SPDX-License-Identifier: GPL-3.0-or-later
// 用内存链路完成两次 GOOSE 发布/接收：业务 false→true，stNum 自动推进。
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mababaNiubi/go-iec61850/data"
	"github.com/mababaNiubi/go-iec61850/examples/internal/memory"
	"github.com/mababaNiubi/go-iec61850/goose"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link := memory.NewLink() // 换成 ethernet.Open 的 Conn 就可接入真实网卡。
	defer link.Close()       // 链路由应用拥有，Publisher/Subscriber 不替你关闭。

	// 对象引用、APPID、VLAN 与两端的数据集顺序必须一致。
	// 此处 MAC 只用于内存演示；真实发布者应使用 conn.MAC()。
	frame := goose.Frame{
		Destination: [6]byte{1, 0x0c, 0xcd, 1, 0, 1}, Source: [6]byte{2, 0, 0, 0, 0, 1},
		APPID: 0x1000, VLAN: &goose.VLAN{Priority: 4, ID: 123},
		PDU: goose.PDU{GoCBRef: "sample/LLN0$GO$events", DataSet: "sample/LLN0$events", ConfRev: 1,
			Values: []data.Value{data.Boolean(false)}},
	}
	// 校验数据集恰好有一个布尔项，接收后才能安全读取 Values[0]。
	types := []data.TypeSpec{{Kind: data.KindBoolean}}
	publisher, err := goose.NewPublisher(link, goose.PublisherConfig{Frame: frame, Types: types})
	if err != nil {
		return err
	}
	vlan := uint16(123)
	subscriber, err := goose.NewSubscriber(goose.SubscriptionConfig{
		Destination: frame.Destination, APPID: frame.APPID, VLANID: &vlan,
		GoCBRef: frame.PDU.GoCBRef, DataSet: frame.PDU.DataSet, ConfRev: 1, Types: types,
	})
	if err != nil {
		return err
	}

	// 第一次发送：库设置 stNum=1、sqNum=0、事件时间和 TAL。
	if err := publisher.Publish(ctx); err != nil {
		return err
	}
	if err := receive(ctx, link, subscriber); err != nil {
		return err
	}

	// 业务数据变化调用 Update；不要直接手改状态号。再次发送成为新状态。
	if _, err := publisher.Update([]data.Value{data.Boolean(true)}); err != nil {
		return err
	}
	if err := publisher.Publish(ctx); err != nil {
		return err
	}
	return receive(ctx, link, subscriber)
}

// receive 展开了接收的两个步骤：解码字节，再用订阅状态机校验/接收。
// 实际持续收包可用 subscriber.Run(ctx, conn) 和 subscriber.Events()。
func receive(ctx context.Context, link goose.FrameIO, subscriber *goose.Subscriber) error {
	packet, err := link.ReadFrame(ctx)
	if err != nil {
		return err
	}
	frame, err := goose.DecodeFrame(packet)
	if err != nil {
		return err
	}
	event, err := subscriber.Process(frame)
	if err != nil {
		return err
	}
	active, err := event.Frame.PDU.Values[0].Bool()
	if err != nil {
		return err
	}
	fmt.Printf("%s: st=%d sq=%d active=%t\n", event.Kind, event.Frame.PDU.StateNumber, event.Frame.PDU.SequenceNumber, active)
	return nil
}
