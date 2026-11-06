// SPDX-License-Identifier: GPL-3.0-or-later
// 发布两个连续 SV 采样；展示明确的数据布局和自动采样计数。
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mababaNiubi/go-iec61850/examples/internal/memory"
	"github.com/mababaNiubi/go-iec61850/sv"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	link := memory.NewLink()
	defer link.Close()

	// 本例布局由应用约定：一个 INT32 测量值 + 一个 32 位 Quality，共 8 字节。
	// seqData 没有类型标签，接收者也必须使用这个布局。
	raw, err := sv.EncodeInt32Quality([]sv.Int32Quality{{Value: 100, Quality: 0}})
	if err != nil {
		return err
	}
	// 下面的MAC仅用于内存演示；真实发布者的Source使用conn.MAC()。
	frame := sv.Frame{
		Destination: [6]byte{1, 0x0c, 0xcd, 4, 0, 1}, Source: [6]byte{2, 0, 0, 0, 0, 1}, APPID: 0x4000,
		PDU: sv.PDU{ASDUs: []sv.ASDU{{SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1, SampleSync: 0, Data: raw}}},
	}
	publisher, err := sv.NewPublisher(link, sv.PublisherConfig{Frame: frame, CounterModulus: 4000})
	if err != nil {
		return err
	}
	subscriber, err := sv.NewSubscriber(sv.SubscriptionConfig{
		Destination: frame.Destination, APPID: frame.APPID, SVID: "sampleSV", DataSet: "sample/LLN0$sv", ConfRev: 1,
		DataLength: 8, CounterModulus: 4000, Timeout: time.Second,
		AllowUnsynchronized: true, // 本例 SampleSync=0；允许软件演示不代表已经同步。
	})
	if err != nil {
		return err
	}

	if err := publisher.Publish(ctx); err != nil {
		return err
	}
	if err := receive(ctx, link, subscriber); err != nil {
		return err
	}

	// 更新第 0 个 ASDU 的采样数据，保持布局长度。成功发送后计数自动加 1。
	raw, err = sv.EncodeInt32Quality([]sv.Int32Quality{{Value: 101, Quality: 0}})
	if err != nil {
		return err
	}
	if err := publisher.UpdateData(0, raw); err != nil {
		return err
	}
	if err := publisher.Publish(ctx); err != nil {
		return err
	}
	return receive(ctx, link, subscriber)
}

func receive(ctx context.Context, link sv.FrameIO, subscriber *sv.Subscriber) error {
	packet, err := link.ReadFrame(ctx)
	if err != nil {
		return err
	}
	frame, err := sv.DecodeFrame(packet)
	if err != nil {
		return err
	}
	events, err := subscriber.Process(frame)
	if err != nil {
		return err
	}
	// 一帧可能有多个匹配 ASDU，所以 Process 返回事件切片，不能只取第一项。
	for _, event := range events {
		values, err := sv.DecodeInt32Quality(event.ASDU.Data)
		if err != nil {
			return err
		}
		fmt.Printf("%s: smpCnt=%d value=%d quality=%#x\n", event.Kind, event.ASDU.SampleCount, values[0].Value, values[0].Quality)
	}
	return nil
}
