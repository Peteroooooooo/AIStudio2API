package aistudio

import (
	"context"
	"testing"
	"time"
)

func TestLastQuotaDayStart(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load LA timezone: %v", err)
	}
	bj := time.FixedZone("CST", 8*3600)

	// 冬令时 (PST, UTC-8): 美西 00:00 对应北京 16:00
	beforePST := time.Date(2026, 1, 15, 15, 59, 0, 0, bj)
	gotBeforePST := LastQuotaDayStart(beforePST).In(loc)
	wantBeforePST := time.Date(2026, 1, 14, 0, 0, 0, 0, loc)
	if !gotBeforePST.Equal(wantBeforePST) {
		t.Fatalf("beforePST = %v, want %v", gotBeforePST, wantBeforePST)
	}

	afterPST := time.Date(2026, 1, 15, 16, 0, 0, 0, bj)
	gotAfterPST := LastQuotaDayStart(afterPST).In(loc)
	wantAfterPST := time.Date(2026, 1, 15, 0, 0, 0, 0, loc)
	if !gotAfterPST.Equal(wantAfterPST) {
		t.Fatalf("afterPST = %v, want %v", gotAfterPST, wantAfterPST)
	}

	// 夏令时 (PDT, UTC-7): 美西 00:00 对应北京 15:00
	afterPDT := time.Date(2026, 7, 15, 15, 0, 0, 0, bj)
	gotAfterPDT := LastQuotaDayStart(afterPDT).In(loc)
	wantAfterPDT := time.Date(2026, 7, 15, 0, 0, 0, 0, loc)
	if !gotAfterPDT.Equal(wantAfterPDT) {
		t.Fatalf("afterPDT = %v, want %v", gotAfterPDT, wantAfterPDT)
	}
}

func smartTestPool(accounts []*Account, perAccount int) *AccountPool {
	pool := NewAccountPool(accounts, perAccount)
	pool.SetUpstreamChannels([]Channel{ChannelPlayground})
	pool.SetRoutingStrategy("smart")
	pool.SetSmartConfig(SmartConfig{SoftRPM: 8, HardRPM: 9, QuotaAutoFreeze: true})
	return pool
}

func TestSmartRoutingIdleFirstAndRPM(t *testing.T) {
	model := Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "ultra@example.com", BenefitTier: BenefitTierUltra, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "free@example.com", BenefitTier: BenefitTierFree, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := smartTestPool(accounts, 2)
	sel := AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent"}

	// 空闲状态先选 Ultra
	lease1, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || lease1 == nil {
		t.Fatalf("lease1=%v err=%v", lease1, err)
	}
	if lease1.Account().ID != "ultra@example.com" {
		t.Fatalf("first pick = %s, want ultra@example.com", lease1.Account().ID)
	}

	// ultra@example.com 正忙时，即使它并发上限为 2 且等级更高，仍应先选完全空闲的 free@example.com
	lease2, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || lease2 == nil {
		t.Fatalf("lease2=%v err=%v", lease2, err)
	}
	if lease2.Account().ID != "free@example.com" {
		t.Fatalf("second pick while ultra busy = %s, want free@example.com", lease2.Account().ID)
	}
	_ = lease1.Release()
	_ = lease2.Release()

	// 连续给 ultra 发送到达 SoftRPM(8) 后，空闲选择应自动切到 free
	pool.SetSmartConfig(SmartConfig{SoftRPM: 2, HardRPM: 3, QuotaAutoFreeze: true})
	for i := range 2 {
		l, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{AccountID: "ultra@example.com", ModelID: "gemini-2.5-pro", Method: "generateContent"})
		if err != nil || l == nil {
			t.Fatalf("warm %d: %v", i, err)
		}
		_ = l.Release()
	}
	lease3, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || lease3 == nil {
		t.Fatalf("lease3=%v err=%v", lease3, err)
	}
	if lease3.Account().ID != "free@example.com" {
		t.Fatalf("pick after ultra hits SoftRPM = %s, want free@example.com", lease3.Account().ID)
	}
	_ = lease3.Release()
}

func TestSmartRoutingSessionStickinessAndRejection(t *testing.T) {
	model := Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := smartTestPool(accounts, 1)
	key := ComputeSessionKey("k1", "gemini-2.5-pro", []string{"sys"}, []Content{{Role: RoleUser, Parts: []Part{{Text: "hello"}}}})
	sel := AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: key}

	l1, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || l1 == nil {
		t.Fatalf("l1=%v err=%v", l1, err)
	}
	first := l1.Account().ID
	_ = l1.Release()
	pool.RecordSmartSuccess(first, 1000)

	// 同会话第二次请求即使 first 今日请求数多 1，也应命中会话粘性继续走 first
	l2, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || l2 == nil {
		t.Fatalf("l2=%v err=%v", l2, err)
	}
	if l2.Account().ID != first {
		t.Fatalf("sticky pick = %s, want %s", l2.Account().ID, first)
	}
	_ = l2.Release()

	// 当 first 触发 403 拒绝后，短时保护锁生效，会话自动避让切到另一个账号
	pool.RecordSmartRejection(first)
	l3, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || l3 == nil {
		t.Fatalf("l3=%v err=%v", l3, err)
	}
	if l3.Account().ID == first {
		t.Fatalf("pick after rejection = %s, want other account", l3.Account().ID)
	}
	_ = l3.Release()
}

func TestStrictQuotaFreezeAndTestBypass(t *testing.T) {
	model := Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := smartTestPool(accounts, 1)

	now := time.Now()
	until := now.Add(2 * time.Hour)
	if _, _, err := pool.MarkQuotaCooldownIfGeneration("a@example.com", "gemini-2.5-pro", 1, now, QuotaCooldown{Until: until, Reason: DailyLimitKind + ": exhausted"}); err != nil {
		t.Fatalf("MarkQuotaCooldownIfGeneration: %v", err)
	}

	statuses := pool.Status()
	if len(statuses) != 1 || statuses[0].QuotaFreeze == nil {
		t.Fatalf("expected QuotaFreeze in status, got %+v", statuses)
	}
	if statuses[0].QuotaFreeze.Kind != DailyLimitKind {
		t.Fatalf("QuotaFreeze.Kind = %q, want %q", statuses[0].QuotaFreeze.Kind, DailyLimitKind)
	}

	// 开启 QuotaAutoFreeze 时，普通调度（即使带 IgnoreQuotaCooldown）也绝不能绕过冻结
	sel := AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", IgnoreQuotaCooldown: true}
	lease, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil {
		t.Fatalf("TryAcquireFor err = %v", err)
	}
	if lease != nil {
		t.Fatalf("expected nil lease when quota frozen, got %v", lease.Account().ID)
	}

	// 仅手动测试（BypassCooldown=true）允许穿透冻结做真实探活
	testSel := AccountSelection{AccountID: "a@example.com", ModelID: "gemini-2.5-pro", Method: "generateContent", BypassCooldown: true}
	testLease, _, err := pool.TryAcquireFor(context.Background(), testSel)
	if err != nil || testLease == nil {
		t.Fatalf("expected testLease with BypassCooldown, got lease=%v err=%v", testLease, err)
	}
	_ = testLease.Release()

	// 测试成功后清理冷却即解冻
	cleared, err := pool.ClearAccountTestCooldowns("a@example.com", "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("ClearAccountTestCooldowns: %v", err)
	}
	if len(cleared) == 0 {
		t.Fatalf("expected len(cleared) > 0")
	}
	if statuses = pool.Status(); statuses[0].QuotaFreeze != nil {
		t.Fatalf("expected QuotaFreeze cleared, got %+v", statuses[0].QuotaFreeze)
	}
	leaseAfter, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent"})
	if err != nil || leaseAfter == nil {
		t.Fatalf("expected lease after unfreeze, got lease=%v err=%v", leaseAfter, err)
	}
	_ = leaseAfter.Release()
}

func TestSmartTrackerCustomSessionTTL(t *testing.T) {
	tracker := newSmartTracker()
	if tracker.effectiveSessionTTL() != 60*time.Minute {
		t.Fatalf("default ttl = %v, want 60m", tracker.effectiveSessionTTL())
	}
	tracker.sessionTTL = 2 * time.Hour
	base := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	tracker.recordDispatch("a@example.com", "sess-1", base)

	// 90 分钟后仍处于 2h 粘性窗口内
	if got := tracker.stickyAccount("sess-1", base.Add(90*time.Minute)); got != "a@example.com" {
		t.Fatalf("sticky at +90m = %q, want a@example.com", got)
	}
	// 超过 2 小时后失效
	if got := tracker.stickyAccount("sess-1", base.Add(2*time.Hour)); got != "" {
		t.Fatalf("sticky at +2h = %q, want empty", got)
	}
}

