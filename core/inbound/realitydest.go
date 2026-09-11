package inbound

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

// realityHandshakeBudget 是 xtls/reality 缓存目标站握手用的固定缓冲
// （该库 tls.go 里的 `size`）。它按 TLS 记录逐条比较
// `5 + 记录长度 > size`，一超就放弃握手。
//
// 【升依赖时必须核对这个值】它原本是 8192，XTLS/REALITY#33 在 2026-09-08 提到
// 17*1024。对不上就会误报——把本来能用的 dest 判成不可用。
// TestBudgetMatchesUpstreamBuffer 会在忘记同步时挂掉。
//
// 【为什么要我们自己查】超限时那边只有一句 `break`，没有任何日志；
// 默认 show=false 的情况下运维只看到
// "REALITY: processed invalid connection: handshake did not complete successfully"，
// 因果被指向客户端。2026-09-11 就是这样：www.microsoft.com 的证书链变长，
// 承载它的记录到了 8273 字节，节点在自己零改动的情况下全盘失效，
// 排查绕了一整轮才定位到目标站。
const realityHandshakeBudget = 17 * 1024

// FirstOversizedRecord 返回第一条会超出预算的 TLS 记录的完整长度
// （含 5 字节记录头），都在预算内时返回 0。
func FirstOversizedRecord(recordLengths []int, budget int) int {
	for _, n := range recordLengths {
		full := n + 5 // 5 字节记录头，和 reality 的算法保持一致
		if full > budget {
			return full
		}
	}
	return 0
}

// InspectRealityDest 和目标站握一次手，回报服务端方向每条 TLS 记录的长度。
func InspectRealityDest(addr, sni string, timeout time.Duration) ([]int, error) {
	raw, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	counter := &recordLenConn{Conn: raw}
	// 这里只量记录长度，不做信任判断，所以跳过证书校验。
	conn := tls.Client(counter, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
	if err := conn.Handshake(); err != nil {
		return counter.lengths, err
	}
	return counter.lengths, nil
}

// CheckRealityDest 在预算被突破时返回一个能直接照着修的错误。
func CheckRealityDest(addr, sni string, timeout time.Duration) error {
	lengths, err := InspectRealityDest(addr, sni, timeout)
	if err != nil && len(lengths) == 0 {
		return fmt.Errorf("REALITY dest %s 握手失败: %w", addr, err)
	}
	if over := FirstOversizedRecord(lengths, realityHandshakeBudget); over > 0 {
		return fmt.Errorf(
			"REALITY dest %s 的握手记录 %d 字节，超过 xtls/reality 的 %d 缓冲上限——"+
				"所有客户端都会连不上，且对端只报 handshake did not complete。换一个证书链更短的 dest",
			addr, over, realityHandshakeBudget)
	}
	return nil
}

// recordLenConn 边读边解析 TLS 记录头，记下服务端方向每条记录的长度。
type recordLenConn struct {
	net.Conn
	hdr     [5]byte
	hdrN    int
	remain  int
	lengths []int
}

func (c *recordLenConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.scan(p[:n])
	}
	return n, err
}

func (c *recordLenConn) scan(b []byte) {
	for len(b) > 0 {
		if c.remain > 0 {
			skip := min(c.remain, len(b))
			c.remain -= skip
			b = b[skip:]
			continue
		}
		need := min(5-c.hdrN, len(b))
		copy(c.hdr[c.hdrN:], b[:need])
		c.hdrN += need
		b = b[need:]
		if c.hdrN == 5 {
			c.remain = int(c.hdr[3])<<8 | int(c.hdr[4])
			c.lengths = append(c.lengths, c.remain)
			c.hdrN = 0
		}
	}
}
