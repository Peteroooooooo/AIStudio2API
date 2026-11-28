package aistudio

import (
	"context"
	"strings"
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

func TestComputeSessionKeyStripsBillingHeaderAndAvoidsTruncationCollision(t *testing.T) {
	// Claude Code 每轮变化 x-anthropic-billing-header 中的 cch=，清洗后应产出完全一致的 SessionKey
	turn1Sys := []string{
		"x-anthropic-billing-header: cc_version=2.1.0; cc_entrypoint=cli; cch=aaa11;",
		"You are an expert assistant.",
	}
	turn2Sys := []string{
		"x-anthropic-billing-header: cc_version=2.1.0; cc_entrypoint=cli; cch=bbb22;",
		"You are an expert assistant.",
	}
	user := []Content{{Role: RoleUser, Parts: []Part{{Text: "Fix bug in router"}}}}
	k1 := ComputeSessionKey("k1", "gemini-2.5-pro", turn1Sys, user)
	k2 := ComputeSessionKey("k1", "gemini-2.5-pro", turn2Sys, user)
	if k1 != k2 {
		t.Fatalf("billing header cch drift changed session key: %q != %q", k1, k2)
	}

	// 并发子代理首条消息共享 >512 字节的 <system-reminder> 前缀，仅尾部任务描述不同，不得碰撞
	sharedPrefix := "<system-reminder>" + strings.Repeat("repo-context-", 60) + "</system-reminder>\n"
	subA := []Content{{Role: RoleUser, Parts: []Part{{Text: sharedPrefix + "Task A: inspect smart.go"}}}}
	subB := []Content{{Role: RoleUser, Parts: []Part{{Text: sharedPrefix + "Task B: inspect runtime.go"}}}}
	ka := ComputeSessionKey("k1", "gemini-2.5-pro", turn1Sys, subA)
	kb := ComputeSessionKey("k1", "gemini-2.5-pro", turn1Sys, subB)
	if ka == kb {
		t.Fatalf("sub-agents sharing >512B prefix collided on session key %q", ka)
	}

	// 显式 sessionHint（如 prompt_cache_key / X-Session-Id）应独立区分会话
	h1 := ComputeSessionKeyForRequest("k1", "gemini-2.5-pro", "subagent-1", turn1Sys, user)
	h2 := ComputeSessionKeyForRequest("k1", "gemini-2.5-pro", "subagent-2", turn1Sys, user)
	if h1 == h2 || h1 == k1 {
		t.Fatalf("explicit session hints should separate sessions: h1=%q h2=%q k1=%q", h1, h2, k1)
	}
}

func TestSmartRoutingSpreadNewSubAgentsAndStickyContinuations(t *testing.T) {
	model := Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "c@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := smartTestPool(accounts, 3)

	// 1. 并发拉起 3 个新 Sub-agent，应分摊到 3 个不同账号
	picked := make(map[string]bool)
	leases := make([]*AccountLease, 0, 3)
	keys := []string{"sub-1", "sub-2", "sub-3"}
	for _, key := range keys {
		l, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
			ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: key,
		})
		if err != nil || l == nil {
			t.Fatalf("new subagent %s: lease=%v err=%v", key, l, err)
		}
		if l.RouteReason() != "new_spread" {
			t.Fatalf("new subagent %s route = %q, want new_spread", key, l.RouteReason())
		}
		if picked[l.Account().ID] {
			t.Fatalf("new subagents piled onto %s instead of spreading", l.Account().ID)
		}
		picked[l.Account().ID] = true
		leases = append(leases, l)
	}
	sub1Account := leases[0].Account().ID
	for _, l := range leases {
		_ = l.Release()
	}

	// 2. 即使 sub1 所在账号此刻正被另一并发请求占用 (active=1 < 3) 且其余两号空闲 (active=0)，sub1 续接轮仍留在原号吃缓存
	blocker, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", AccountID: sub1Account,
	})
	if err != nil || blocker == nil {
		t.Fatalf("blocker: lease=%v err=%v", blocker, err)
	}
	defer blocker.Release()

	contSession, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "sub-1",
	})
	if err != nil || contSession == nil {
		t.Fatalf("contSession: lease=%v err=%v", contSession, err)
	}
	if contSession.Account().ID != sub1Account || contSession.RouteReason() != "session_sticky" {
		t.Fatalf("sticky continuation got account=%s route=%q, want %s/session_sticky", contSession.Account().ID, contSession.RouteReason(), sub1Account)
	}
	_ = contSession.Release()

	// TurnAccountID 同样优先命中原号且标记为 turn_sticky
	contTurn, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "other-key", TurnAccountID: sub1Account,
	})
	if err != nil || contTurn == nil {
		t.Fatalf("contTurn: lease=%v err=%v", contTurn, err)
	}
	if contTurn.Account().ID != sub1Account || contTurn.RouteReason() != "turn_sticky" {
		t.Fatalf("turn sticky got account=%s route=%q, want %s/turn_sticky", contTurn.Account().ID, contTurn.RouteReason(), sub1Account)
	}
	_ = contTurn.Release()

	// 3. 超过 SoftRPM 但未到 HardRPM 时：新会话分摊走开，粘性续接轮继续留原号
	pool.SetSmartConfig(SmartConfig{SoftRPM: 2, HardRPM: 10, QuotaAutoFreeze: true})
	newSess, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "brand-new",
	})
	if err != nil || newSess == nil {
		t.Fatalf("newSess: lease=%v err=%v", newSess, err)
	}
	if newSess.Account().ID == sub1Account {
		t.Fatalf("new session should spread away from busy/soft-RPM account %s", sub1Account)
	}
	_ = newSess.Release()

	contOverSoft, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "sub-1",
	})
	if err != nil || contOverSoft == nil {
		t.Fatalf("contOverSoft: lease=%v err=%v", contOverSoft, err)
	}
	if contOverSoft.Account().ID != sub1Account {
		t.Fatalf("sticky continuation over SoftRPM (<HardRPM) = %s, want %s", contOverSoft.Account().ID, sub1Account)
	}
	_ = contOverSoft.Release()
}

func TestSmartRoutingOverflowPreservesHealthyStickyAndFailoverMigrates(t *testing.T) {
	model := Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*Account{
		{ID: "a@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
		{ID: "b@example.com", BenefitTier: BenefitTierPro, Config: AccountConfig{Enabled: true}, State: AccountReady, Models: []Model{model}, modelAccessGeneration: 1},
	}
	pool := smartTestPool(accounts, 1)
	sel := AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "sess-sticky"}

	// 首轮绑定到 a
	l1, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || l1 == nil {
		t.Fatalf("l1=%v err=%v", l1, err)
	}
	home := l1.Account().ID
	_ = l1.Release()

	// 1. 临时满并发溢出：home 被占满 -> 溢出到另一号，但绝不覆写 sess-sticky 的原归属
	busy, _, err := pool.TryAcquireFor(context.Background(), AccountSelection{
		ModelID: "gemini-2.5-pro", Method: "generateContent", AccountID: home,
	})
	if err != nil || busy == nil {
		t.Fatalf("busy=%v err=%v", busy, err)
	}
	overflow, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || overflow == nil {
		t.Fatalf("overflow=%v err=%v", overflow, err)
	}
	if overflow.Account().ID == home || overflow.RouteReason() != "overflow_busy" || overflow.StickyTarget() != home {
		t.Fatalf("overflow got account=%s route=%q target=%q, want other/overflow_busy/%s", overflow.Account().ID, overflow.RouteReason(), overflow.StickyTarget(), home)
	}
	_ = overflow.Release()
	_ = busy.Release()

	// home 空闲后，下一轮立刻回到 home 吃原缓存
	returned, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || returned == nil {
		t.Fatalf("returned=%v err=%v", returned, err)
	}
	if returned.Account().ID != home || returned.RouteReason() != "session_sticky" {
		t.Fatalf("after overflow got account=%s route=%q, want %s/session_sticky", returned.Account().ID, returned.RouteReason(), home)
	}
	_ = returned.Release()

	// 2. 触发 HardRPM 溢出：同样保留原归属，记为 overflow_rpm
	pool.SetSmartConfig(SmartConfig{SoftRPM: 1, HardRPM: 2, QuotaAutoFreeze: true})
	rpmOverflow, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || rpmOverflow == nil {
		t.Fatalf("rpmOverflow=%v err=%v", rpmOverflow, err)
	}
	if rpmOverflow.Account().ID == home || rpmOverflow.RouteReason() != "overflow_rpm" || rpmOverflow.StickyTarget() != home {
		t.Fatalf("rpmOverflow got account=%s route=%q target=%q, want other/overflow_rpm/%s", rpmOverflow.Account().ID, rpmOverflow.RouteReason(), rpmOverflow.StickyTarget(), home)
	}
	_ = rpmOverflow.Release()
	if resolved, _ := pool.ResolveStickyAccount(sel); resolved != home {
		t.Fatalf("sticky after rpm overflow = %q, want %q", resolved, home)
	}

	// 3. home 发生 403 拒绝：执行故障迁移 failover，且把粘性迁移到新号
	pool.SetSmartConfig(SmartConfig{SoftRPM: 8, HardRPM: 9, QuotaAutoFreeze: true})
	pool.RecordSmartRejection(home)
	failover, _, err := pool.TryAcquireFor(context.Background(), sel)
	if err != nil || failover == nil {
		t.Fatalf("failover=%v err=%v", failover, err)
	}
	newHome := failover.Account().ID
	if newHome == home || failover.RouteReason() != "failover" || failover.StickyTarget() != home {
		t.Fatalf("failover got account=%s route=%q target=%q, want other/failover/%s", newHome, failover.RouteReason(), failover.StickyTarget(), home)
	}
	_ = failover.Release()
	if resolved, _ := pool.ResolveStickyAccount(sel); resolved != newHome {
		t.Fatalf("sticky after failover = %q, want %q", resolved, newHome)
	}
}

func TestModelTurnHistoryAccountLookupAndTokenIsolation(t *testing.T) {
	turns := newModelTurnHistory()
	turn1User := []Content{{Role: RoleUser, Parts: []Part{{Text: "Start task"}}}}
	if got := turns.lastAccount("k1", turn1User); got != "" {
		t.Fatalf("turn 1 lastAccount = %q, want empty", got)
	}
	_, _, tip1 := turns.restore("k1", "a@example.com", turn1User)
	turns.store("k1", tip1, modelTurn{
		parts:     []Part{{Text: "Plan ready."}},
		turnToken: "tok-a",
		accountID: "a@example.com",
	})

	turn2 := []Content{
		{Role: RoleUser, Parts: []Part{{Text: "Start task"}}},
		{Role: RoleAssistant, Parts: []Part{{Text: "Plan ready."}}},
		{Role: RoleUser, Parts: []Part{{Text: "Proceed"}}},
	}
	if got := turns.lastAccount("k1", turn2); got != "a@example.com" {
		t.Fatalf("turn 2 lastAccount = %q, want a@example.com", got)
	}
	// 同账户恢复应带回 turnToken，跨账户（溢出/故障迁移）恢复必须清空 turnToken
	_, tokSame, tip2 := turns.restore("k1", "a@example.com", turn2)
	if tokSame != "tok-a" {
		t.Fatalf("same-account restore token = %q, want tok-a", tokSame)
	}
	_, tokOther, _ := turns.restore("k1", "b@example.com", turn2)
	if tokOther != "" {
		t.Fatalf("cross-account restore token = %q, want empty", tokOther)
	}

	// 分叉会话：分支 A 由 a 生成，分支 B 由 b 生成，最长前缀链分别命中对应账户
	turns.store("k1", tip2, modelTurn{
		parts:     []Part{{Text: "Done on B."}},
		turnToken: "tok-b",
		accountID: "b@example.com",
	})
	turn3B := append(append([]Content(nil), turn2...),
		Content{Role: RoleAssistant, Parts: []Part{{Text: "Done on B."}}},
		Content{Role: RoleUser, Parts: []Part{{Text: "Verify"}}},
	)
	if got := turns.lastAccount("k1", turn3B); got != "b@example.com" {
		t.Fatalf("branch B lastAccount = %q, want b@example.com", got)
	}
}

