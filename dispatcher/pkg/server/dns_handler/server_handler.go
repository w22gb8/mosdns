//     Copyright (C) 2020-2021, IrineSistiana
//
//     This file is part of mosdns.
//
//     mosdns is free software: you can redistribute it and/or modify
//     it under the terms of the GNU General Public License as published by
//     the Free Software Foundation, either version 3 of the License, or
//     (at your option) any later version.
//
//     mosdns is distributed in the hope that it will be useful,
//     but WITHOUT ANY WARRANTY; without even the implied warranty of
//     MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//     GNU General Public License for more details.
//
//     You should have received a copy of the GNU General Public License
//     along with this program.  If not, see <https://www.gnu.org/licenses/>.

package dns_handler

import (
	"context"
	"github.com/IrineSistiana/mosdns/v3/dispatcher/handler"
	"github.com/IrineSistiana/mosdns/v3/dispatcher/pkg/concurrent_limiter"
	"github.com/IrineSistiana/mosdns/v3/dispatcher/pkg/pool"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"sync"
	"testing"
	"time"
)

const (
	defaultQueryTimeout = time.Second * 5
)

// Handler handles dns query.
type Handler interface {
	// ServeDNS handles r and writes response to w.
	// meta may be nil.
	ServeDNS(ctx context.Context, req []byte, w ResponseWriter, meta *handler.RequestMeta)
}

// ResponseWriter can write msg to the client.
type ResponseWriter interface {
	Write(m []byte) (n int, err error)
}

type DefaultHandler struct {
	// Logger is used for logging. A nil value will disable logging.
	Logger *zap.Logger

	// Entry is the entry ExecutablePlugin's tag. This cannot be nil.
	Entry handler.ExecutableChainNode

	// QueryTimeout limits the timeout value of each query.
	// Default is defaultQueryTimeout.
	QueryTimeout time.Duration

	// ConcurrentLimit controls the max concurrent queries for the DefaultHandler.
	// If ConcurrentLimit <= 0, means no limit.
	// When calling DefaultHandler.ServeDNS(), if a query exceeds the limit, it will wait on a FIFO queue until
	// - its ctx is done or currently there are more than 8 x ConcurrentLimit queries waiting -> The query will be dropped silently.
	// - it can be proceeded -> Normal procedure.
	ConcurrentLimit int

	// RecursionAvailable sets the dns.Msg.RecursionAvailable flag globally.
	RecursionAvailable bool

	initOnce sync.Once                             // init limiter
	limiter  *concurrent_limiter.ConcurrentLimiter // if it's nil, means no limit.
}

var (
	nopLogger = zap.NewNop()
)

// ServeDNS implements Handler.
// If entry returns an error, a SERVFAIL response will be sent back to client.
// If concurrentLimit is reached, the query will block and wait available token until ctx is done.
func (h *DefaultHandler) ServeDNS(ctx context.Context, req []byte, w ResponseWriter, meta *handler.RequestMeta) {
	h.initOnce.Do(func() {
		if h.ConcurrentLimit > 0 {
			h.limiter = concurrent_limiter.NewConcurrentLimiter(h.ConcurrentLimit, h.ConcurrentLimit*8)
		}
	})

	// apply timeout to ctx
	ddl := time.Now().Add(h.queryTimeout())
	ctxDdl, ok := ctx.Deadline()
	if !(ok && ctxDdl.Before(ddl)) {
		newCtx, cancel := context.WithDeadline(ctx, ddl)
		defer cancel()
		ctx = newCtx
	}

	if h.limiter != nil {
		if !h.limiter.Wait() { // too many waiting query, silently drop it.
			return
		}
		defer h.limiter.WaitDone()

		select {
		case h.limiter.Run() <- struct{}{}:
			defer h.limiter.RunDone()
		case <-ctx.Done():
			return // ctx timeout, silently drop it.
		}
	}

	reqMsg := new(dns.Msg)
	if err := reqMsg.Unpack(req); err != nil {
		h.logger().Warn("failed to unpack request message", zap.Any("meta", meta), zap.Binary("data", req))
		return
	}

	qCtx := handler.NewContext(reqMsg, meta)
	err := handler.ExecChainNode(ctx, qCtx, h.Entry)
	if err != nil {
		h.logger().Warn("entry returned an err", qCtx.InfoField(), zap.Error(err))
	} else {
		h.logger().Debug("entry returned", qCtx.InfoField(), zap.Stringer("status", qCtx.Status()))
	}

	var rm *dns.Msg
	if err != nil || qCtx.Status() == handler.ContextStatusServerFailed {
		rm = new(dns.Msg)
		rm.SetReply(reqMsg)
		rm.Rcode = dns.RcodeServerFailure
	} else {
		rm = qCtx.R()
	}

	if rm != nil {
		if h.RecursionAvailable {
			rm.RecursionAvailable = true
		}
		raw, buf, err := pool.PackBuffer(rm)
		if err != nil {
			h.logger().Warn("failed to pack response message", qCtx.InfoField(), zap.Error(err))
			return
		}
		defer pool.ReleaseBuf(buf)

		if _, err := w.Write(raw); err != nil {
			h.logger().Warn("failed to write response", qCtx.InfoField(), zap.Error(err))
		}
	}
}

func (h *DefaultHandler) queryTimeout() time.Duration {
	if t := h.QueryTimeout; t > 0 {
		return t
	}
	return defaultQueryTimeout
}

func (h *DefaultHandler) logger() *zap.Logger {
	if l := h.Logger; l != nil {
		return l
	}
	return nopLogger
}

type DummyServerHandler struct {
	T       *testing.T
	WantMsg *dns.Msg
	WantErr error
}

func (d *DummyServerHandler) ServeDNS(_ context.Context, req []byte, w ResponseWriter, meta *handler.RequestMeta) {
	q := new(dns.Msg)
	if err := q.Unpack(req); err != nil {
		return
	}

	var resp *dns.Msg
	if d.WantMsg != nil {
		resp = d.WantMsg.Copy()
		resp.Id = q.Id
	} else {
		resp = new(dns.Msg)
		resp.SetReply(q)
	}

	raw, err := resp.Pack()
	if err != nil {
		d.T.Error(err)
		return
	}

	_, err = w.Write(raw)
	if err != nil {
		d.T.Error(err)
		return
	}
}
