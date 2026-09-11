package inbound

import "testing"

// 预算跟着 xtls/reality 的 tls.go 里的 size 走，升依赖时必须一起核对。
// 2026-09-08 XTLS/REALITY#33 把它从 8192 提到了 17*1024。
func TestBudgetMatchesUpstreamBuffer(t *testing.T) {
	if realityHandshakeBudget != 17*1024 {
		t.Fatalf("realityHandshakeBudget = %d，和依赖里的 size 对不上了；"+
			"升 xtls/reality 之后要同步这个常量", realityHandshakeBudget)
	}
}

func TestFirstOversizedRecordFlagsRecordOverBudget(t *testing.T) {
	lengths := []int{127, 6, 53, 17404} // 17404+5 = 17409，刚过线
	if got := FirstOversizedRecord(lengths, realityHandshakeBudget); got != 17409 {
		t.Fatalf("FirstOversizedRecord = %d, want 17409（记录长度要含 5 字节头）", got)
	}
}

// www.microsoft.com 那条 8273 字节的记录，在 8192 时代会打挂整条链路
// （XTLS/Xray-core#6356），升到 17KiB 之后应当放行。
func TestFirstOversizedRecordAcceptsFormerlyFatalMicrosoftChain(t *testing.T) {
	lengths := []int{127, 6, 53, 8268} // 8268+5 = 8273
	if got := FirstOversizedRecord(lengths, realityHandshakeBudget); got != 0 {
		t.Fatalf("FirstOversizedRecord = %d, want 0（8273 在 17KiB 预算内）", got)
	}
}

func TestFirstOversizedRecordReportsTheFirstOffender(t *testing.T) {
	lengths := []int{20000, 30000}
	if got := FirstOversizedRecord(lengths, realityHandshakeBudget); got != 20005 {
		t.Fatalf("FirstOversizedRecord = %d, want 20005（第一条，不是最大的那条）", got)
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
			c.scan(stream[i:min(i+chunk, len(stream))])
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
	}
}
