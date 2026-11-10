package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

// This file is a small Model Context Protocol server over "streamable HTTP":
// the client POSTs JSON-RPC messages and gets JSON back. Only tools are
// offered, nothing is pushed from the server, so no event stream or session is
// needed.

const (
	mcpLatestVersion = "2025-06-18"
	maxRequestBytes  = 64 << 20
	serverName       = "caddy-snake-apps"
)

var mcpSupportedVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

// ServerVersion is shown in the dashboard and reported to MCP clients. Keep it
// equal to the version in cmd/cli/pyproject.toml (see Releases in AGENTS.md).
var ServerVersion = "0.7.0"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// publishFailed reports whether a tool result carries a publish that did not
// go through, so the agent is told the call needs attention.
func publishFailed(result any) bool {
	switch v := result.(type) {
	case *PublishResult:
		return v != nil && !v.OK
	case *DraftResult:
		return v != nil && v.Publish != nil && !v.Publish.OK
	case map[string]any:
		if res, ok := v["publish"].(*PublishResult); ok {
			return res != nil && !res.OK
		}
	}
	return false
}

func (p *Platform) mcpDispatch(ctx context.Context, who string, req rpcRequest) (result any, rpcErr *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := mcpLatestVersion
		if mcpSupportedVersions[params.ProtocolVersion] {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": serverName, "title": "caddy-snake apps", "version": ServerVersion},
			"instructions":    Guide,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		type listed struct {
			Tool
			Annotations map[string]any `json:"annotations"`
		}
		tools := []listed{}
		for _, t := range Tools() {
			tools = append(tools, listed{Tool: t, Annotations: map[string]any{"destructiveHint": t.Destructive}})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "tools/call needs a tool name"}
		}
		out, err := p.Call(ctx, params.Name, who, params.Arguments)
		if err != nil {
			// Tool failures are results the agent can read and act on, not protocol errors.
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			}, nil
		}
		text, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, &rpcError{Code: -32603, Message: err.Error()}
		}
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
			"isError": publishFailed(out),
		}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

// ServeMCP handles one MCP HTTP request on behalf of who.
func (p *Platform) ServeMCP(w http.ResponseWriter, r *http.Request, who string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "this endpoint accepts POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusBadRequest)
		return
	}
	trimmed := bytes.TrimSpace(body)
	var requests []rpcRequest
	batch := len(trimmed) > 0 && trimmed[0] == '['
	if batch {
		err = json.Unmarshal(trimmed, &requests)
	} else {
		var one rpcRequest
		err = json.Unmarshal(trimmed, &one)
		requests = []rpcRequest{one}
	}
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "invalid JSON"}})
		return
	}
	var responses []rpcResponse
	for _, req := range requests {
		isNotification := len(req.ID) == 0 || string(req.ID) == "null"
		if isNotification {
			continue // notifications (such as notifications/initialized) get no reply
		}
		result, rpcErr := p.mcpDispatch(r.Context(), who, req)
		responses = append(responses, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr})
	}
	if len(responses) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if batch {
		_ = json.NewEncoder(w).Encode(responses)
		return
	}
	_ = json.NewEncoder(w).Encode(responses[0])
}
