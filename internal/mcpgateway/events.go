package mcpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jaysongiroux/hook_way/internal/flowconfig"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type eventParams struct {
	mcp.ParamsBase
	Raw json.RawMessage
}

func (p *eventParams) UnmarshalJSON(b []byte) error {
	p.Raw = append(p.Raw[:0], b...)
	var metadata struct {
		Meta mcp.Meta `json:"_meta"`
	}
	if err := json.Unmarshal(b, &metadata); err != nil {
		return err
	}
	p.Meta = metadata.Meta
	return nil
}

type eventResult struct {
	mcp.ResultBase
	value any
}

func (r *eventResult) MarshalJSON() ([]byte, error) { return json.Marshal(r.value) }
func (g *Gateway) registerEvents(s *mcp.Server) error {
	if len(g.opts.Config.Events) == 0 {
		return nil
	}
	if g.opts.Events == nil {
		return errors.New("MCP events runtime required")
	}
	for _, method := range []string{"events/list", "events/subscribe", "events/unsubscribe"} {
		if err := mcp.AddReceivingCustomMethod(s, method, func(ctx context.Context, _ *mcp.ServerSession, q *eventParams) (*eventResult, error) {
			p, ok := ctx.Value(principalKey{}).(principal)
			if !ok || p.Anonymous {
				return nil, &jsonrpc.Error{Code: -32024, Message: "Forbidden"}
			}
			raw := json.RawMessage(`{}`)
			if q != nil {
				raw = q.Raw
			}
			result, err := g.opts.Events.EventCall(ctx, g.opts.Endpoint, method, toEventPrincipal(p), raw)
			if err != nil {
				var e *flowconfig.EventRPCError
				if errors.As(err, &e) {
					data, _ := json.Marshal(map[string]string{"reason": e.Reason})
					return nil, &jsonrpc.Error{Code: int64(e.Code), Message: e.Message, Data: data}
				}
				return nil, &jsonrpc.Error{Code: -32603, Message: "Event operation failed"}
			}
			return &eventResult{value: result}, nil
		}); err != nil {
			return err
		}
	}
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "events/list" || method == "events/subscribe" || method == "events/unsubscribe" {
				if version, ok := req.(interface{ ProtocolVersion() string }); !ok || version.ProtocolVersion() != "2026-07-28" {
					return nil, &jsonrpc.Error{Code: -32026, Message: "MCP events require protocol 2026-07-28"}
				}
			}
			result, err := next(ctx, method, req)
			if err != nil || method != "server/discover" {
				return result, err
			}
			p, _ := ctx.Value(principalKey{}).(principal)
			visible := false
			for _, e := range g.opts.Config.Events {
				visible = visible || flowconfig.EventAllowed(toEventPrincipal(p), e.Access)
			}
			if !visible {
				return result, nil
			}
			raw, err := json.Marshal(result)
			if err != nil {
				return nil, err
			}
			var projected map[string]any
			if err = json.Unmarshal(raw, &projected); err != nil {
				return nil, err
			}
			caps, _ := projected["capabilities"].(map[string]any)
			if caps == nil {
				caps = map[string]any{}
				projected["capabilities"] = caps
			}
			caps["events"] = map[string]any{}
			projected["resultType"] = "complete"
			return &eventResult{value: projected}, nil
		}
	})
	return nil
}
func toEventPrincipal(p principal) flowconfig.EventPrincipal {
	return flowconfig.EventPrincipal{Issuer: p.Issuer, ID: p.ID, Key: p.Key, Scopes: p.Scopes, Roles: p.Roles, ExpiresAt: p.ExpiresAt}
}
func (g *Gateway) eventPrincipalValid(ctx context.Context, p flowconfig.EventPrincipal) bool {
	if p.ID == "" || (!p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)) {
		return false
	}
	if p.Key != "" {
		for _, k := range g.auth.keys {
			if k.name == p.Key && (k.expires.IsZero() || time.Now().Before(k.expires)) {
				v, err := k.resolver.Secrets(ctx)
				return err == nil && len(v) > 0 && p.ID == "key:"+p.Key
			}
		}
		return false
	}
	return g.auth.opts.OAuth != nil && p.Issuer == g.auth.opts.OAuth.Issuer && !p.ExpiresAt.IsZero()
}
