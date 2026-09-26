package service

import (
	"context"
	"net"
)

// Endpoint dial helper.
// 渠道监控允许管理员显式配置 HTTP、本地 IP、内网 IP 和自定义域名，因此
// 不再做公网/私网或 hostname 黑名单拦截；请求仍受 transport/client 超时控制。

// monitorDialer 共享 Dialer，与 net/http 默认值对齐。
var monitorDialer = &net.Dialer{
	Timeout:   monitorDialTimeout,
	KeepAlive: monitorDialKeepAlive,
}

// safeDialContext 保留为自定义 transport 的入口，实际策略交由管理员配置的
// endpoint 和 HTTP 客户端超时控制。
func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return monitorDialer.DialContext(ctx, network, address)
}
