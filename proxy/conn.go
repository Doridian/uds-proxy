package proxy

import (
	"context"
	"net"
	"net/http"
)

type proxyContextKey struct {
	key string
}

var netConnContextKey = &proxyContextKey{"http:net.Conn"}

func ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, netConnContextKey, c)
}

func GetNetConn(r *http.Request) net.Conn {
	return r.Context().Value(netConnContextKey).(net.Conn)
}
