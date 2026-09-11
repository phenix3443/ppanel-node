package inbound

import "testing"

// REALITY 缓存目标站握手的缓冲是写死的 8192（xtls/reality tls.go:140 的 size），
// 超出时它直接 break、不留任何日志，运维只看到 "handshake did not complete
// successfully"，因果被指向客户端。这两个测试锁住我们自己的预检逻辑。
func TestFirstOversizedRecordFlagsChainOverBudget(t *testing.T) {
	// 2026-09-11 www.microsoft.com 实测：承载证书的那条记录 8273 > 8192。
	lengths := []int{127, 6, 53, 8268}
	got := FirstOversizedRecord(lengths, realityHandshakeBudget)
	if got != 8273 {
		t.Fatalf("FirstOversizedRecord = %d, want 8273 (记录长度要含 5 字节头)", got)
	}
}

func TestFirstOversizedRecordAcceptsChainWithinBudget(t *testing.T) {
	// dl.google.com 那一档，全部记录都在预算内。
	lengths := []int{127, 6, 53, 4912}
	if got := FirstOversizedRecord(lengths, realityHandshakeBudget); got != 0 {
		t.Fatalf("FirstOversizedRecord = %d, want 0", got)
	}
}

func TestFirstOversizedRecordReportsTheFirstOffender(t *testing.T) {
	lengths := []int{9000, 20000}
	if got := FirstOversizedRecord(lengths, realityHandshakeBudget); got != 9005 {
		t.Fatalf("FirstOversizedRecord = %d, want 9005（第一条，不是最大的那条）", got)
	}
}

// recordLenConn 要能处理「一条记录跨多次 Read」和「一次 Read 里有多条记录」，
// 这两种在真实网络上都会发生，算错就会漏掉超限的那条。
func TestRecordLenConnHandlesSplitAndBatchedRecords(t *testing.T) {
	rec := func(payload int) []byte {
		b := []byte{0x16, 0x03, 0x03, byte(payload >> 8), byte(payload)}
		return append(b, make([]byte, payload)...)
	}
	stream := append(rec(127), rec(6)...)
	stream = append(stream, rec(8268)...)

	for _, chunk := range []int{1, 3, 5, 7, 4096, len(stream)} {
		c := &recordLenConn{}
		for i := 0; i < len(stream); i += chunk {
			end := min(i+chunk, len(stream))
			c.scan(stream[i:end])
		}
		want := []int{127, 6, 8268}
		if len(c.lengths) != len(want) {
			t.Fatalf("chunk=%d: 解析出 %v, want %v", chunk, c.lengths, want)
		}
		for i := range want {
			if c.lengths[i] != want[i] {
				t.Fatalf("chunk=%d: 解析出 %v, want %v", chunk, c.lengths, want)
			}
		}
		if got := FirstOversizedRecord(c.lengths, realityHandshakeBudget); got != 8273 {
			t.Fatalf("chunk=%d: FirstOversizedRecord = %d, want 8273", chunk, got)
		}
	}
}
