// fake_plugin 是测试用的最小 JSON-RPC 子进程。
//
// 协议：
//   - 收到 {"method":"initialize"} → 回 {"id":N,"result":{}}
//   - 收到 {"method":"shutdown"} → 回 {"id":N,"result":{}} 然后退出
//   - 其他消息 → 回 {"id":N,"error":{"code":-32601,"message":"method not found"}}
//
// 用法：go run ./examples/fake_plugin
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			// notification，忽略
			continue
		}
		var resp Response
		resp.JSONRPC = "2.0"
		resp.ID = *req.ID
		switch req.Method {
		case "initialize":
			resp.Result = map[string]any{"status": "ok"}
		case "shutdown":
			resp.Result = map[string]any{"status": "bye"}
			out, _ := json.Marshal(resp)
			fmt.Fprintln(os.Stdout, string(out))
			os.Exit(0)
		default:
			// 简单 result，避免 error 路径
			resp.Result = map[string]any{"status": "ignored"}
		}
		out, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(out))
	}
}
