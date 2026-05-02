// futu.go — 富途 OpenD 轻量 Go 客户端
//
// 零外部依赖。自实现 TCP 帧协议 + Protobuf 编解码，
// 仅覆盖 guanfu 需要的 3 个消息类型：
//   InitConnect → Qot_RequestHistoryKL → Qot_GetSecuritySnapshot
//
// 协议参考: https://openapi.futunn.com/futu-api-doc/
// OpenD 默认地址: 127.0.0.1:11111
//
// 用法:
//
//	c, _ := futuConnect("127.0.0.1:11111")
//	defer c.Close()
//	kl, _ := c.RequestHistoryKL("US.SPY", 3000)
//	snap, _ := c.GetSnapshot("US.QQQ")

package client

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sync"
	"time"
)

// ─── TCP 帧协议 ───────────────────────────────────────

// Futu 包格式 (44 字节头 + protobuf body)
//
//	[0..3]   uint32  总长 = 44 + len(body)
//	[4..7]   uint32  头长 = 44
//	[8..11]  uint32  包类型 = 0 (Protobuf)
//	[12..15] uint32  协议版本 = 1
//	[16..19] uint32  序列号
//	[20..23] uint32  body 长度
//	[24..31] uint64  保留
//	[32..43] [12]byte 保留

const (
	futuHeaderLen = 44
	futuProtoType = 0
	futuProtoVer  = 1
)

type futuConn struct {
	conn     net.Conn
	serialNo uint32
	mu       sync.Mutex
	connID   uint64
}

func futuConnect(addr string) (*futuConn, error) {
	if addr == "" {
		addr = "127.0.0.1:11111"
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("futu dial %s: %w", addr, err)
	}
	fc := &futuConn{conn: conn}

	// InitConnect
	resp, err := fc.sendAndRecv(1001, encodeInitConnect()) // 1001 = InitConnect
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("futu InitConnect: %w", err)
	}
	if id := pbGetVarint(resp, 1); id > 0 {
		fc.connID = id
	}
	return fc, nil
}

func (c *futuConn) Close() error { return c.conn.Close() }

func futuAddr() string {
	if addr := os.Getenv("FUTU_GATEWAY"); addr != "" {
		return addr
	}
	return "127.0.0.1:11111"
}

// ─── K 线请求 ─────────────────────────────────────────

// FutuKLPoint 单根 K 线
type FutuKLPoint struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// RequestHistoryKL 请求历史日 K 线
// symbol: "US.QQQ", "US.SPY", "US.GLD"
// days: 需要的天数 (最多 3000，单次 1000 封顶，自动分页)
func (c *futuConn) RequestHistoryKL(symbol string, days int) ([]FutuKLPoint, error) {
	if days <= 0 {
		days = 1000
	}
	const maxPerReq = 1000
	var all []FutuKLPoint
	page := 0

	for len(all) < days {
		n := days - len(all)
		if n > maxPerReq {
			n = maxPerReq
		}
		body := encodeRequestHistoryKL(symbol, int32(page*n), int32(n))
		resp, err := c.sendAndRecv(3102, body) // 3102 = Qot_RequestHistoryKL
		if err != nil {
			return all, err
		}
		points, hasMore := decodeKLineResponse(resp)
		all = append(all, points...)
		if !hasMore || len(points) < n {
			break
		}
		page++
	}
	return all, nil
}

// RequestSnapPrice 获取股票最新价 (简化快照)
func (c *futuConn) RequestSnapPrice(symbol string) (float64, string, error) {
	body := encodeGetSnapshot(symbol)
	resp, err := c.sendAndRecv(3101, body) // 3101 = Qot_GetSecuritySnapshot
	if err != nil {
		return 0, "", err
	}
	return decodeSnapshotPrice(resp)
}

// ─── 底层 send/recv ──────────────────────────────────

func (c *futuConn) sendAndRecv(cmd uint32, body []byte) ([]byte, error) {
	c.mu.Lock()
	c.serialNo++
	sn := c.serialNo
	c.mu.Unlock()

	if err := futuWrite(c.conn, cmd, sn, body); err != nil {
		return nil, err
	}
	return futuRead(c.conn)
}

func futuWrite(conn net.Conn, cmd uint32, sn uint32, body []byte) error {
	totalLen := futuHeaderLen + len(body)
	buf := make([]byte, totalLen)

	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalLen))
	binary.LittleEndian.PutUint32(buf[4:8], futuHeaderLen)
	binary.LittleEndian.PutUint32(buf[8:12], futuProtoType)
	binary.LittleEndian.PutUint32(buf[12:16], futuProtoVer)
	binary.LittleEndian.PutUint32(buf[16:20], sn)
	binary.LittleEndian.PutUint32(buf[20:24], uint32(len(body)))
	// 24..43: reserved (zero)

	copy(buf[44:], body)

	// 前面再加 4 字节 totalLen (Futu 的 pre-header)
	full := make([]byte, 4+totalLen)
	binary.LittleEndian.PutUint32(full[0:4], uint32(totalLen))
	copy(full[4:], buf)

	_, err := conn.Write(full)
	return err
}

func futuRead(conn net.Conn) ([]byte, error) {
	// 读 pre-header (4 bytes)
	pre := make([]byte, 4)
	if _, err := io.ReadFull(conn, pre); err != nil {
		return nil, err
	}
	totalLen := binary.LittleEndian.Uint32(pre)
	if totalLen < futuHeaderLen || totalLen > 10*1024*1024 {
		return nil, fmt.Errorf("futu bad packet len: %d", totalLen)
	}

	// 读 header + body
	full := make([]byte, totalLen)
	copy(full[0:4], pre)
	if _, err := io.ReadFull(conn, full[4:]); err != nil {
		return nil, err
	}

	bodyLen := binary.LittleEndian.Uint32(full[20:24])
	if futuHeaderLen+bodyLen > totalLen {
		return nil, fmt.Errorf("futu body overflow: %d > %d", futuHeaderLen+bodyLen, totalLen)
	}

	body := full[futuHeaderLen : futuHeaderLen+bodyLen]

	// 检查 retType
	retType := pbGetVarint(body, 1)
	if retType != 0 {
		retMsg := pbGetString(body, 2)
		return nil, fmt.Errorf("futu error: retType=%d msg=%s", retType, retMsg)
	}

	return body, nil
}

// ─── Protobuf 编码器 (mini) ───────────────────────────

// 我们只实现 field type 0 (varint) 和 type 2 (length-delimited)。
// Futu 消息只用了这两种 wire type。

func pbTag(fieldNum uint32, wireType byte) uint64 {
	return uint64(fieldNum<<3 | uint32(wireType))
}

func pbVarint(v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	return buf[:n+1]
}

func pbEncodeVarint(buf []byte, fieldNum uint32, v uint64) []byte {
	buf = append(buf, pbVarint(pbTag(fieldNum, 0))...)
	buf = append(buf, pbVarint(v)...)
	return buf
}

func pbEncodeString(buf []byte, fieldNum uint32, s string) []byte {
	buf = append(buf, pbVarint(pbTag(fieldNum, 2))...)
	buf = append(buf, pbVarint(uint64(len(s)))...)
	buf = append(buf, s...)
	return buf
}

func pbEncodeDouble(buf []byte, fieldNum uint32, v float64) []byte {
	buf = append(buf, pbVarint(pbTag(fieldNum, 1))...) // wire type 1 = 64-bit
	u := math.Float64bits(v)
	buf = append(buf, byte(u), byte(u>>8), byte(u>>16), byte(u>>24),
		byte(u>>32), byte(u>>40), byte(u>>48), byte(u>>56))
	return buf
}

func pbEncodeBool(buf []byte, fieldNum uint32, v bool) []byte {
	val := uint64(0)
	if v {
		val = 1
	}
	return pbEncodeVarint(buf, fieldNum, val)
}

// nested message: length-delimited sub-message
func pbEncodeMessage(buf []byte, fieldNum uint32, sub []byte) []byte {
	buf = append(buf, pbVarint(pbTag(fieldNum, 2))...)
	buf = append(buf, pbVarint(uint64(len(sub)))...)
	buf = append(buf, sub...)
	return buf
}

// ─── Protobuf 解码器 (mini) ───────────────────────────

// pbGetVarint 从 bytes 中取 fieldNum 的第一个 varint 值
func pbGetVarint(body []byte, fieldNum uint32) uint64 {
	v, _ := pbFindVarint(body, fieldNum)
	return v
}

// pbGetString 从 bytes 中取 fieldNum 的 string 值
func pbGetString(body []byte, fieldNum uint32) string {
	_, rest := pbSkipField(body)
	for len(rest) > 0 {
		fn, wt, rest := pbReadTag(rest)
		if fn == fieldNum && wt == 2 {
			length, n := decodeVarint(rest)
			if n > 0 && int(length) <= len(rest[n:]) {
				return string(rest[n : n+int(length)])
			}
		}
		rest = pbSkipFieldValue(wt, rest)
	}
	return ""
}

// pbGetDouble 取 double 值
func pbGetDouble(body []byte, fieldNum uint32) float64 {
	for len(body) > 0 {
		fn, wt, rest := pbReadTag(body)
		if fn == fieldNum && wt == 1 {
			u := binary.LittleEndian.Uint64(rest)
			return math.Float64frombits(u)
		}
		body = pbSkipFieldValue(wt, rest)
	}
	return 0
}

// pbGetNested 取嵌套消息字节
func pbGetNested(body []byte, fieldNum uint32) []byte {
	for len(body) > 0 {
		fn, wt, rest := pbReadTag(body)
		if fn == fieldNum && wt == 2 {
			length, n := decodeVarint(rest)
			if n > 0 && int(length) <= len(rest[n:]) {
				return rest[n : n+int(length)]
			}
		}
		body = pbSkipFieldValue(wt, rest)
	}
	return nil
}

func pbReadTag(data []byte) (fieldNum uint32, wireType byte, rest []byte) {
	v, n := decodeVarint(data)
	if n <= 0 {
		return 0, 0, data
	}
	return uint32(v >> 3), byte(v & 0x7), data[n:]
}

func decodeVarint(data []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 10 && i < len(data); i++ {
		b := data[i]
		v |= uint64(b&0x7F) << (7 * i)
		if b < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

func pbSkipFieldValue(wt byte, data []byte) []byte {
	switch wt {
	case 0: // varint
		_, n := decodeVarint(data)
		return data[n:]
	case 1: // 64-bit
		if len(data) >= 8 {
			return data[8:]
		}
	case 2: // length-delimited
		length, n := decodeVarint(data)
		if n > 0 && int(length) <= len(data[n:]) {
			return data[n+int(length):]
		}
	case 5: // 32-bit
		if len(data) >= 4 {
			return data[4:]
		}
	}
	return nil
}

func pbFindVarint(body []byte, fieldNum uint32) (uint64, []byte) {
	for len(body) > 0 {
		fn, wt, rest := pbReadTag(body)
		if fn == fieldNum && wt == 0 {
			v, n := decodeVarint(rest)
			return v, rest[n:]
		}
		body = pbSkipFieldValue(wt, rest)
	}
	return 0, nil
}

func pbSkipField(body []byte) (uint64, []byte) {
	_, wt, rest := pbReadTag(body)
	rest = pbSkipFieldValue(wt, rest)
	return 0, rest
}

// ─── 消息编码 ─────────────────────────────────────────

// Futu 命令号 (C2S):
//
//	1001 = InitConnect
//	3102 = Qot_RequestHistoryKL
//	3101 = Qot_GetSecuritySnapshot

// encodeInitConnect → InitConnect.C2S
//
//	message InitConnect {
//	  string clientID = 1;    // 客户端标识
//	  int32  clientVer = 2;   // 客户端版本 (0 = 无需加密)
//	  bool   recvNotify = 3;  // 是否接收推送
//	}
func encodeInitConnect() []byte {
	var buf []byte
	buf = pbEncodeString(buf, 1, "guanfu")
	buf = pbEncodeVarint(buf, 2, 1)   // clientVer
	buf = pbEncodeBool(buf, 3, false) // recvNotify
	return buf
}

// encodeRequestHistoryKL → Qot_RequestHistoryKL.C2S
//
//	message Security { int32 market=1; string code=2; }
//	message C2S {
//	  Security security = 1;
//	  int32    rehabType = 2;  // 1=前复权
//	  int32    klType = 3;     // 2=日K
//	  int32    num = 6;
//	}
func encodeRequestHistoryKL(symbol string, start, num int32) []byte {
	market, code, err := parseFutuSymbol(symbol)
	if err != nil {
		return nil
	}

	// Security sub-message
	var sec []byte
	sec = pbEncodeVarint(sec, 1, uint64(market))
	sec = pbEncodeString(sec, 2, code)

	var buf []byte
	buf = pbEncodeMessage(buf, 1, sec)     // security
	buf = pbEncodeVarint(buf, 2, 1)        // rehabType = 前复权
	buf = pbEncodeVarint(buf, 3, 2)        // klType = 日K
	buf = pbEncodeVarint(buf, 6, uint64(num)) // num
	return buf
}

// encodeGetSnapshot → Qot_GetSecuritySnapshot.C2S
func encodeGetSnapshot(symbol string) []byte {
	market, code, err := parseFutuSymbol(symbol)
	if err != nil {
		return nil
	}
	var sec []byte
	sec = pbEncodeVarint(sec, 1, uint64(market))
	sec = pbEncodeString(sec, 2, code)

	var buf []byte
	buf = pbEncodeMessage(buf, 1, sec)
	return buf
}

func parseFutuSymbol(symbol string) (market int32, code string, err error) {
	// US.QQQ → market=11, code="QQQ"
	// HK.00700 → market=1, code="00700"
	switch {
	case len(symbol) > 3 && symbol[:3] == "US.":
		return 11, symbol[3:], nil
	case len(symbol) > 3 && symbol[:3] == "HK.":
		return 1, symbol[3:], nil
	case len(symbol) > 4 && symbol[:4] == "USNY":
		return 11, symbol[4:], nil
	default:
		return 0, "", fmt.Errorf("unknown futu symbol: %s", symbol)
	}
}

// ─── 消息解码 ─────────────────────────────────────────

// decodeKLineResponse 解析 Qot_RequestHistoryKL.Ret
//
//	Ret { repeated KLine klList = 12; }
//	KLine { string time=1; double open=4; double high=5; double low=6;
//	        double close=7; double volume=10; }
func decodeKLineResponse(body []byte) ([]FutuKLPoint, bool) {
	// s2c → field 1 (Security), field 5 (klList array)
	klList := pbGetNested(body, 5) // field 5 = repeated KLine
	if klList == nil {
		return nil, false
	}

	var points []FutuKLPoint
	// 遍历 klList 中的每条 KLine (嵌套消息)
	for len(klList) > 0 {
		fn, wt, rest := pbReadTag(klList)
		if fn == 0 {
			break
		}
		if fn == 2 && wt == 2 { // field 5 -> repeated -> each element is field 5? Actually no...
			// In repeated messages, each element is embedded as length-delimited
			length, n := decodeVarint(rest)
			if n > 0 && int(length) <= len(rest[n:]) {
				kline := rest[n : n+int(length)]
				points = append(points, decodeKLine(kline))
				klList = rest[n+int(length):]
				continue
			}
		}
		klList = pbSkipFieldValue(wt, rest)
	}

	// hasMore → field 8
	nextPage := pbGetVarint(body, 8)
	return points, nextPage == 1
}

func decodeKLine(data []byte) FutuKLPoint {
	var p FutuKLPoint
	timeStr := pbGetString(data, 1)
	if t, err := time.Parse("2006-01-02", timeStr); err == nil {
		p.Time = t
	} else if t, err := time.Parse("2006-01-02 15:04:05", timeStr); err == nil {
		p.Time = t
	}
	p.Open = pbGetDouble(data, 4)
	p.High = pbGetDouble(data, 5)
	p.Low = pbGetDouble(data, 6)
	p.Close = pbGetDouble(data, 7)
	// volume is field 10 (double) or field 14 (int64)
	p.Volume = pbGetDouble(data, 10)
	if p.Volume == 0 {
		p.Volume = float64(pbGetVarint(data, 14))
	}
	return p
}

// decodeSnapshotPrice 解析 Qot_GetSecuritySnapshot.Ret
func decodeSnapshotPrice(body []byte) (float64, string, error) {
	// snapshot → field 4 (BasicInfo) → field 2 (curPrice)
	snapshot := pbGetNested(body, 4)
	if snapshot == nil {
		return 0, "", errors.New("futu snapshot missing BasicInfo")
	}
	price := pbGetDouble(snapshot, 2)
	asOf := pbGetString(snapshot, 5) // updateTime
	return price, asOf, nil
}

// ─── 公开接口 ─────────────────────────────────────────

// CrossAssetFutuPrices holds prices fetched from Futu OpenD
type CrossAssetFutuPrices struct {
	QQQPrice     float64
	QQQHistory   []float64
	QQQPriceAsOf string
	SPYPrice     float64
	SPYHistory   []float64
	SPYPriceAsOf  string
	GLDPrice     float64 // GLD ETF (实物黄金 ETF，补充 PAXG)
	GLDHistory   []float64
	GLDPriceAsOf string
	Warnings     []string
}

// FetchCrossAssetFromFutu 从本地富途网关拉取 QQQ/SPY 数据
func FetchCrossAssetFromFutu(days int) (*CrossAssetFutuPrices, error) {
	if days <= 0 {
		days = 1000
	}

	c, err := futuConnect(futuAddr())
	if err != nil {
		return nil, fmt.Errorf("futu connect: %w", err)
	}
	defer c.Close()

	out := &CrossAssetFutuPrices{}

	// QQQ
	qqqKL, err := c.RequestHistoryKL("US.QQQ", days)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("futu QQQ KL: %v", err))
	} else if len(qqqKL) > 0 {
		out.QQQHistory = klToFloat64(qqqKL)
		out.QQQPrice = qqqKL[0].Close
		out.QQQPriceAsOf = qqqKL[0].Time.Format("2006-01-02")
	}

	// SPY
	spyKL, err := c.RequestHistoryKL("US.SPY", days)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("futu SPY KL: %v", err))
	} else if len(spyKL) > 0 {
		out.SPYHistory = klToFloat64(spyKL)
		out.SPYPrice = spyKL[0].Close
		out.SPYPriceAsOf = spyKL[0].Time.Format("2006-01-02")
	}

	// GLD (实物黄金 ETF，可选)
	gldKL, err := c.RequestHistoryKL("US.GLD", days)
	if err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("futu GLD KL: %v", err))
	} else if len(gldKL) > 0 {
		out.GLDHistory = klToFloat64(gldKL)
		out.GLDPrice = gldKL[0].Close
		out.GLDPriceAsOf = gldKL[0].Time.Format("2006-01-02")
	}

	return out, nil
}

func klToFloat64(kl []FutuKLPoint) []float64 {
	out := make([]float64, len(kl))
	for i, k := range kl {
		out[i] = k.Close
	}
	return out
}
