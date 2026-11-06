// SPDX-License-Identifier: GPL-3.0-or-later
// Package mmsdemo 只负责给示例提供临时的本机 MMS 对端。
// 业务 API 调用保留在各个示例 main.go；本包不属于应用需要导入的库。
package mmsdemo

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/mababaNiubi/go-iec61850/mms"
)

// Start 在 address 留空时启动回环服务；传入地址时直接使用现有设备。
// onAssociation 仅用于本机报告示例，必须在 Serve 前设置。
// 返回的 stop 关闭本示例创建的服务，不会关闭远端设备。
func Start(address string, onAssociation func(*mms.Association)) (string, func(), error) {
	if address != "" {
		return address, func() {}, nil
	}
	model := mms.NewModel()
	integer := mms.TypeSpec{Kind: mms.KindInteger, Width: 32}
	variables := []mms.Variable{
		{Name: mms.DomainName("sample", "int"), Type: integer, Value: mms.Integer(42), Writable: true},
		{Name: mms.DomainName("sample", "bool"), Type: mms.TypeSpec{Kind: mms.KindBoolean}, Value: mms.Boolean(true), Writable: true},
		{Name: mms.DomainName("sample", "float"), Type: mms.TypeSpec{Kind: mms.KindFloat, Width: 32, ExponentWidth: 8}, Value: mms.Float32(1.25), Writable: true},
		{Name: mms.DomainName("sample", "text"), Type: mms.TypeSpec{Kind: mms.KindVisibleString, Length: -32}, Value: mms.VisibleString("hello"), Writable: true},
		// Writable 的零值为 false，用来演示逐项访问失败。
		{Name: mms.DomainName("sample", "readonly"), Type: integer, Value: mms.Integer(9)},
		{Name: mms.DomainName("sample", "array"), Type: mms.TypeSpec{Kind: mms.KindArray, Length: 3, Element: &integer}, Value: mms.Array(mms.Integer(1), mms.Integer(2), mms.Integer(3)), Writable: true},
		{Name: mms.DomainName("sample", "struct"), Type: mms.TypeSpec{Kind: mms.KindStructure, Components: []mms.Component{
			{Name: "flag", Type: mms.TypeSpec{Kind: mms.KindBoolean}}, {Name: "count", Type: integer},
		}}, Value: mms.Structure(mms.Boolean(true), mms.Integer(7)), Writable: true},
	}
	for _, variable := range variables {
		if err := model.Add(variable); err != nil {
			return "", nil, err
		}
	}
	server, err := mms.NewServer(model, mms.Config{})
	if err != nil {
		return "", nil, err
	}
	server.OnAssociation = onAssociation
	// 端口 0 由系统分配，多个示例并行执行时不会抢占固定端口。
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	stop := func() {
		// 清理使用新的 context，不能复用可能已经超时的业务 context。
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("关闭示例服务: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				log.Printf("示例服务: %v", err)
			}
		case <-ctx.Done():
			log.Printf("等待示例服务退出: %v", ctx.Err())
		}
	}
	return listener.Addr().String(), stop, nil
}
