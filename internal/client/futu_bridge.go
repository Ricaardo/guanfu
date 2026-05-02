// futu_bridge.go — Python bridge for Futu OpenD
//
// OpenD 默认启用 RSA+AES 加密握手，官方只有 Python/Java/C#/C++ SDK。
// 我们的自写 Go 客户端 (futu.go) 未实现加密层，无法直接连接。
//
// 此处通过调用官方 Python SDK 的桥接脚本获取数据：
//   futu_bridge.py 接收 JSON stdin → 调用 futu-api → 输出 JSON stdout
//
// 未来可考虑用 Go 实现完整加密握手 (~200 行 crypto/rsa+crypto/aes)，
// 届时此文件可删除，futu.go 直连 OpenD。

package client

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// futuBridgeSymbols 通过 Python 桥接脚本获取多个标的的历史 K 线和最新价
func futuBridgeSymbols(symbols []string, days int) (*CrossAssetFutuPrices, error) {
	script := futuBridgePath()
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("futu_bridge.py not found at %s: %w", script, err)
	}

	input := map[string]interface{}{
		"symbols": symbols,
		"days":    days,
	}
	stdin, _ := json.Marshal(input)

	cmd := exec.Command("python3", script)
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8")
	w, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("futu_bridge stdin: %w", err)
	}
	// Write stdin before starting so it's ready
	if _, err := w.Write(stdin); err != nil {
		w.Close()
		return nil, fmt.Errorf("futu_bridge write: %w", err)
	}
	w.Close()

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("futu_bridge.py: %w (stderr may have details)", err)
	}

	var result map[string]struct {
		Price   float64   `json:"price"`
		History []float64 `json:"history"`
		AsOf    string    `json:"as_of"`
		Error   string    `json:"error"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("futu_bridge.py JSON: %w\n%s", err, string(out[:min(len(out), 200)]))
	}

	out2 := &CrossAssetFutuPrices{}
	for sym, r := range result {
		if r.Error != "" {
			out2.Warnings = append(out2.Warnings, fmt.Sprintf("futu_bridge %s: %s", sym, r.Error))
			continue
		}
		switch sym {
		case "US.QQQ":
			out2.QQQPrice, out2.QQQHistory, out2.QQQPriceAsOf = r.Price, r.History, r.AsOf
		case "US.SPY":
			out2.SPYPrice, out2.SPYHistory, out2.SPYPriceAsOf = r.Price, r.History, r.AsOf
		case "US.GLD":
			out2.GLDPrice, out2.GLDHistory, out2.GLDPriceAsOf = r.Price, r.History, r.AsOf
		case "US.UUP":
			out2.UUPPrice, out2.UUPHistory, out2.UUPPriceAsOf = r.Price, r.History, r.AsOf
		case "US.TLT":
			out2.TLTPrice, out2.TLTHistory, out2.TLTPriceAsOf = r.Price, r.History, r.AsOf
		case "US.VIXY":
			out2.VIXYPrice, out2.VIXYHistory, out2.VIXYPriceAsOf = r.Price, r.History, r.AsOf
		}
	}
	return out2, nil
}

func futuBridgePath() string {
	if p := os.Getenv("FUTU_BRIDGE"); p != "" {
		return p
	}
	// Try next to binary first, then CWD, then source dir
	candidates := []string{
		filepath.Join(filepath.Dir(os.Args[0]), "futu_bridge.py"),
		"futu_bridge.py",
		filepath.Join("internal", "client", "futu_bridge.py"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "futu_bridge.py" // let exec fail with clear error
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
