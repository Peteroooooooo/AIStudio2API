package aistudio

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	smartRPMWindow        = time.Minute
	smartSessionTTL       = 60 * time.Minute
	smartMaxSessions      = 2048
	smartPenaltyHalfLife  = 15.0 // minutes
	smartRejectionHoldDur = 30 * time.Second
)

// SmartConfig 控制智能路由（ROUTING_STRATEGY=smart）的限频、会话粘性与自动冻结参数
type SmartConfig struct {
	SoftRPM         int
	HardRPM         int
	SessionTTL      time.Duration
	QuotaAutoFreeze bool
}

type smartAccountState struct {
	recentDispatches   []time.Time
	dailyCycleStart    time.Time
	dailyRequests      int64
	dailyTokens        int64
	rejectionPenalty   float64
	rejectionHoldUntil time.Time
	penaltyUpdatedAt   time.Time
	hasRollingQuota    bool
}

type smartSessionAffinity struct {
	accountID string
	expiresAt time.Time
}

type smartTracker struct {
	softRPM         int
	hardRPM         int
	sessionTTL      time.Duration
	quotaAutoFreeze bool
	accounts        map[string]*smartAccountState
	sessions        map[string]smartSessionAffinity
}

func newSmartTracker() *smartTracker {
	return &smartTracker{
		softRPM:         8,
		hardRPM:         9,
		sessionTTL:      smartSessionTTL,
		quotaAutoFreeze: true,
		accounts:        make(map[string]*smartAccountState),
		sessions:        make(map[string]smartSessionAffinity),
	}
}

func (t *smartTracker) effectiveSessionTTL() time.Duration {
	if t.sessionTTL > 0 {
		return t.sessionTTL
	}
	return smartSessionTTL
}

func (t *smartTracker) accountState(accountID string, now time.Time) *smartAccountState {
	if t.accounts == nil {
		t.accounts = make(map[string]*smartAccountState)
	}
	st := t.accounts[accountID]
	if st == nil {
		st = &smartAccountState{
			dailyCycleStart: LastQuotaDayStart(now),
		}
		t.accounts[accountID] = st
		return st
	}
	cycleStart := LastQuotaDayStart(now)
	if st.dailyCycleStart.Before(cycleStart) {
		st.dailyCycleStart = cycleStart
		st.dailyRequests = 0
		st.dailyTokens = 0
	}
	return st
}

func (t *smartTracker) pruneDispatches(st *smartAccountState, now time.Time) int {
	cutoff := now.Add(-smartRPMWindow)
	n := 0
	for _, ts := range st.recentDispatches {
		if ts.After(cutoff) {
			st.recentDispatches[n] = ts
			n++
		}
	}
	st.recentDispatches = st.recentDispatches[:n]
	return n
}

func (t *smartTracker) decayedPenalty(st *smartAccountState, now time.Time) float64 {
	if st.rejectionPenalty <= 0 || st.penaltyUpdatedAt.IsZero() {
		return 0
	}
	elapsed := now.Sub(st.penaltyUpdatedAt).Minutes()
	if elapsed <= 0 {
		return st.rejectionPenalty
	}
	decayed := st.rejectionPenalty * math.Pow(0.5, elapsed/smartPenaltyHalfLife)
	if decayed < 0.01 {
		st.rejectionPenalty = 0
		st.penaltyUpdatedAt = now
		return 0
	}
	return decayed
}

func (t *smartTracker) recordRPM(accountID string, now time.Time) {
	st := t.accountState(accountID, now)
	t.pruneDispatches(st, now)
	st.recentDispatches = append(st.recentDispatches, now)
}

func (t *smartTracker) recordDispatch(accountID string, sessionKey string, now time.Time) {
	t.recordRPM(accountID, now)
	if sessionKey != "" {
		t.setSession(sessionKey, accountID, now)
	}
}

func (t *smartTracker) clearSessionIfAccount(sessionKey string, accountID string) {
	if sessionKey == "" || accountID == "" || t.sessions == nil {
		return
	}
	if entry, ok := t.sessions[sessionKey]; ok && entry.accountID == accountID {
		delete(t.sessions, sessionKey)
	}
}

func (t *smartTracker) setSession(sessionKey string, accountID string, now time.Time) {
	if t.sessions == nil {
		t.sessions = make(map[string]smartSessionAffinity)
	}
	if len(t.sessions) >= smartMaxSessions {
		for key, item := range t.sessions {
			if !now.Before(item.expiresAt) {
				delete(t.sessions, key)
			}
		}
		if len(t.sessions) >= smartMaxSessions {
			for key := range t.sessions {
				delete(t.sessions, key)
				if len(t.sessions) < smartMaxSessions/2 {
					break
				}
			}
		}
	}
	t.sessions[sessionKey] = smartSessionAffinity{
		accountID: accountID,
		expiresAt: now.Add(t.effectiveSessionTTL()),
	}
}

func (t *smartTracker) stickyAccount(sessionKey string, now time.Time) string {
	if sessionKey == "" || t.sessions == nil {
		return ""
	}
	entry, ok := t.sessions[sessionKey]
	if !ok {
		return ""
	}
	if !now.Before(entry.expiresAt) {
		delete(t.sessions, sessionKey)
		return ""
	}
	return entry.accountID
}

func (t *smartTracker) recordSuccess(accountID string, totalTokens int64, now time.Time) {
	st := t.accountState(accountID, now)
	st.dailyRequests++
	if totalTokens > 0 {
		st.dailyTokens += totalTokens
	}
	current := t.decayedPenalty(st, now)
	if current > 0 {
		st.rejectionPenalty = current * 0.5
		st.penaltyUpdatedAt = now
	}
	st.rejectionHoldUntil = time.Time{}
}

func (t *smartTracker) recordRejection(accountID string, now time.Time) {
	st := t.accountState(accountID, now)
	current := t.decayedPenalty(st, now)
	st.rejectionPenalty = min(current+100.0, 500.0)
	st.penaltyUpdatedAt = now
	holdUntil := now.Add(smartRejectionHoldDur)
	if holdUntil.After(st.rejectionHoldUntil) {
		st.rejectionHoldUntil = holdUntil
	}
}

func (t *smartTracker) recordTransientFailure(accountID string, now time.Time) {
	st := t.accountState(accountID, now)
	current := t.decayedPenalty(st, now)
	st.rejectionPenalty = min(current+25.0, 500.0)
	st.penaltyUpdatedAt = now
}

func (t *smartTracker) clearPenalty(accountID string, now time.Time) {
	st := t.accountState(accountID, now)
	st.rejectionPenalty = 0
	st.rejectionHoldUntil = time.Time{}
	st.penaltyUpdatedAt = now
}

func (t *smartTracker) markRollingQuota(accountID string, now time.Time) {
	st := t.accountState(accountID, now)
	st.hasRollingQuota = true
}

func (t *smartTracker) seedDailyUsage(accountID string, requests int64, tokens int64, now time.Time) {
	st := t.accountState(accountID, now)
	if requests > st.dailyRequests {
		st.dailyRequests = requests
	}
	if tokens > st.dailyTokens {
		st.dailyTokens = tokens
	}
}

// ComputeSessionKey 生成用于保持隐式提示词缓存命中的稳定会话粘性键
func ComputeSessionKey(apiKey string, modelID string, system []string, contents []Content) string {
	return ComputeSessionKeyForRequest(apiKey, modelID, "", system, contents)
}

// ComputeSessionKeyForRequest 在剥离客户端动态头（如计费头 cch=）与拒绝句后，基于完整稳定系统提示与首条用户消息（或显式会话键）生成会话粘性键，避免动态头漂移与 512 字节截断导致的并发子代理碰撞
func ComputeSessionKeyForRequest(apiKey string, modelID string, sessionHint string, system []string, contents []Content) string {
	h := sha256.New()
	writePart := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{'|'})
	}
	writePart(strings.TrimSpace(apiKey))
	writePart(strings.TrimPrefix(strings.TrimSpace(modelID), "models/"))
	if hint := strings.TrimSpace(sessionHint); hint != "" {
		writePart("hint")
		writePart(hint)
		return hex.EncodeToString(h.Sum(nil)[:12])
	}
	cleanedSystem := clientSystem(system)
	writePart(strings.TrimSpace(strings.Join(cleanedSystem, "\n")))
	cleanedContents := dropUserRejected(contents)
	for _, content := range cleanedContents {
		if content.Role != RoleUser {
			continue
		}
		text := strings.TrimSpace(contentsText([]Content{content}))
		if text == "" {
			continue
		}
		writePart(text)
		break
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// SetSmartConfig 更新智能路由的限频与自动冻结配置
func (p *AccountPool) SetSmartConfig(cfg SmartConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	p.smart.softRPM = max(cfg.SoftRPM, 0)
	p.smart.hardRPM = max(cfg.HardRPM, 0)
	if cfg.SessionTTL > 0 {
		p.smart.sessionTTL = cfg.SessionTTL
	}
	p.smart.quotaAutoFreeze = cfg.QuotaAutoFreeze
	p.notifyLocked()
}

// IsSmartRouting 返回当前账户选择策略是否为 smart
func (p *AccountPool) IsSmartRouting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.routingStrategy == "smart"
}

// QuotaAutoFreeze 返回限额后是否严格冻结到恢复时间
func (p *AccountPool) QuotaAutoFreeze() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.smart == nil || p.smart.quotaAutoFreeze
}

// SeedDailyUsage 在启动或重置时从请求账本灌入账户今日请求量与 token 量
func (p *AccountPool) SeedDailyUsage(byLabel map[string]struct{ Requests, Tokens int64 }, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	for _, account := range p.accounts {
		key := strings.ToLower(strings.TrimSpace(account.Config.Label))
		if stats, ok := byLabel[key]; ok {
			p.smart.seedDailyUsage(account.ID, stats.Requests, stats.Tokens, now)
		} else if stats, ok := byLabel[strings.ToLower(account.ID)]; ok {
			p.smart.seedDailyUsage(account.ID, stats.Requests, stats.Tokens, now)
		}
	}
}

// RecordSmartSuccess 记录账户请求成功，累加日用量并衰减惩罚分
func (p *AccountPool) RecordSmartSuccess(accountID string, totalTokens int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	p.smart.recordSuccess(accountID, totalTokens, time.Now())
}

// RecordSmartSuccessByLabel 按账户标签或 ID 记录请求成功，累加当日用量并衰减惩罚分
func (p *AccountPool) RecordSmartSuccessByLabel(label string, totalTokens int64) {
	label = strings.TrimSpace(label)
	if label == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	now := time.Now()
	if account := p.byID[label]; account != nil {
		p.smart.recordSuccess(account.ID, totalTokens, now)
		return
	}
	for _, account := range p.accounts {
		if strings.EqualFold(account.Config.Label, label) {
			p.smart.recordSuccess(account.ID, totalTokens, now)
			return
		}
	}
}

// RecordSmartRejection 记录账户触发 403 权限拒绝，写入短时保护与健康惩罚分
func (p *AccountPool) RecordSmartRejection(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	p.smart.recordRejection(accountID, time.Now())
}

// RecordSmartFailure 记录账户发生非额度失败（计入每日请求次数并微调惩罚）
func (p *AccountPool) RecordSmartFailure(accountID string, isQuota bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	now := time.Now()
	st := p.smart.accountState(accountID, now)
	st.dailyRequests++
	if !isQuota {
		p.smart.recordTransientFailure(accountID, now)
	}
}

// ClearSmartPenalty 清除账户的健康惩罚与短时保护锁（用于手动测试成功后）
func (p *AccountPool) ClearSmartPenalty(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart != nil {
		p.smart.clearPenalty(accountID, time.Now())
	}
}

// ResolveStickyAccount 返回选号命中的粘性账户及其来源（"turn" 或 "session"）；无粘性时返回空串
func (p *AccountPool) ResolveStickyAccount(selection AccountSelection) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stickyTargetLocked(selection, time.Now())
}

// HasStickyAccount 返回选号是否具备有效的历史轮或会话粘性目标
func (p *AccountPool) HasStickyAccount(selection AccountSelection) bool {
	id, _ := p.ResolveStickyAccount(selection)
	return id != ""
}

// StickyAccountEligible 判断粘性账户当前是否健康、未冷却、未达硬 RPM 且有可用并发槽位
func (p *AccountPool) StickyAccountEligible(selection AccountSelection, accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	account := p.byID[accountID]
	if account == nil {
		return false
	}
	if selection.AllowedAccountIDs != nil && !slices.Contains(selection.AllowedAccountIDs, accountID) {
		return false
	}
	if !p.stickyAccountHealthyLocked(accountID, selection, now) {
		return false
	}
	if account.exclusive || account.exclusiveWaiters > 0 || account.authRefreshers > 0 ||
		account.active >= p.perAccountConcurrency || p.assistantBusyLocked(account, selection) {
		return false
	}
	if p.smart != nil && p.smart.hardRPM > 0 {
		st := p.smart.accountState(accountID, now)
		if p.smart.pruneDispatches(st, now) >= p.smart.hardRPM {
			return false
		}
	}
	return true
}

// ConfirmSmartSession 在请求成功收到上游首事件后确认或刷新会话粘性，不覆盖其他仍健康的原粘性账号
func (p *AccountPool) ConfirmSmartSession(sessionKey string, accountID string, modelID string) {
	sessionKey = strings.TrimSpace(sessionKey)
	accountID = strings.TrimSpace(accountID)
	if sessionKey == "" || accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart == nil {
		return
	}
	now := time.Now()
	prev := p.smart.stickyAccount(sessionKey, now)
	if prev == "" || prev == accountID || !p.stickyAccountHealthyLocked(prev, AccountSelection{ModelID: modelID, Method: "generateContent"}, now) {
		p.smart.setSession(sessionKey, accountID, now)
	}
}

// InvalidateSmartSession 在账户尝试于首事件前失败时清除指向该失败账户的会话粘性，避免后续请求继续命中故障号
func (p *AccountPool) InvalidateSmartSession(sessionKey string, accountID string) {
	sessionKey = strings.TrimSpace(sessionKey)
	accountID = strings.TrimSpace(accountID)
	if sessionKey == "" || accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.smart != nil {
		p.smart.clearSessionIfAccount(sessionKey, accountID)
	}
}

func (p *AccountPool) stickyTargetLocked(selection AccountSelection, now time.Time) (string, string) {
	if id := strings.TrimSpace(selection.TurnAccountID); id != "" {
		return id, "turn"
	}
	if p.smart != nil && selection.SessionKey != "" {
		if id := p.smart.stickyAccount(selection.SessionKey, now); id != "" {
			return id, "session"
		}
	}
	return "", ""
}

func (p *AccountPool) stickyAccountHealthyLocked(accountID string, selection AccountSelection, now time.Time) bool {
	account := p.byID[accountID]
	if account == nil || !account.Config.Enabled || account.State != AccountReady {
		return false
	}
	if selection.ModelID != "" && !p.accountSupportsAnyChannelLocked(account, selection) {
		return false
	}
	if _, cooling := p.accountChannelCooldownLocked(account, selection, now); cooling {
		return false
	}
	if p.smart != nil {
		st := p.smart.accountState(accountID, now)
		if !selection.BypassCooldown && now.Before(st.rejectionHoldUntil) {
			return false
		}
		if p.smart.decayedPenalty(st, now) >= 10 {
			return false
		}
	}
	return true
}

// recordSmartDispatchLocked 记录账户 60s RPM 窗口并在保护健康原粘性的前提下更新会话归属，同时返回本次选号原因与原粘性目标
func (p *AccountPool) recordSmartDispatchLocked(account *Account, selection AccountSelection, now time.Time) (string, string) {
	if selection.BypassCooldown || p.smart == nil {
		return "", ""
	}
	stickyID, stickySource := p.stickyTargetLocked(selection, now)
	p.smart.recordRPM(account.ID, now)
	if p.routingStrategy != "smart" {
		if selection.SessionKey != "" {
			p.smart.setSession(selection.SessionKey, account.ID, now)
		}
		return "", ""
	}
	if selection.AccountID != "" || selection.ResourceID != "" {
		return "fixed", ""
	}
	if stickyID == "" {
		if selection.SessionKey != "" {
			p.smart.setSession(selection.SessionKey, account.ID, now)
		}
		return "new_spread", ""
	}
	if account.ID == stickyID {
		if selection.SessionKey != "" {
			p.smart.setSession(selection.SessionKey, account.ID, now)
		}
		if stickySource == "turn" {
			return "turn_sticky", ""
		}
		return "session_sticky", ""
	}
	// 实际选中的账户不是原粘性账户：原账户仍健康时视为临时溢出，绝不覆写原会话粘性；原账户已故障/冷却时执行迁移
	if p.stickyAccountHealthyLocked(stickyID, selection, now) {
		st := p.smart.accountState(stickyID, now)
		rpm60 := p.smart.pruneDispatches(st, now)
		if p.smart.hardRPM > 0 && rpm60 >= p.smart.hardRPM {
			return "overflow_rpm", stickyID
		}
		return "overflow_busy", stickyID
	}
	if selection.SessionKey != "" {
		p.smart.setSession(selection.SessionKey, account.ID, now)
	}
	return "failover", stickyID
}

func (p *AccountPool) sortSmartIndicesLocked(indices []int, selection AccountSelection, now time.Time) {
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	stickyID, _ := p.stickyTargetLocked(selection, now)
	scores := make(map[string]float64, len(indices))
	for _, idx := range indices {
		account := p.accounts[idx]
		scores[account.ID] = p.smartScoreLocked(account, selection, stickyID, now)
	}
	sort.SliceStable(indices, func(left, right int) bool {
		leftID := p.accounts[indices[left]].ID
		rightID := p.accounts[indices[right]].ID
		ls, rs := scores[leftID], scores[rightID]
		if ls != rs {
			return ls > rs
		}
		lr, rr := p.accountRankLocked(leftID), p.accountRankLocked(rightID)
		if lr != rr {
			return lr < rr
		}
		return leftID < rightID
	})
}

func (p *AccountPool) smartScoreLocked(account *Account, selection AccountSelection, stickyID string, now time.Time) float64 {
	st := p.smart.accountState(account.ID, now)
	rpm60 := p.smart.pruneDispatches(st, now)
	penalty := p.smart.decayedPenalty(st, now)
	rejected := !selection.BypassCooldown && now.Before(st.rejectionHoldUntil)
	hardLimited := p.smart.hardRPM > 0 && rpm60 >= p.smart.hardRPM
	stickyEligible := stickyID != "" && account.ID == stickyID && !rejected && !hardLimited && penalty < 10

	var score float64

	// 1. 403 账户级短时保护锁
	if rejected {
		score -= 1_000_000
	}

	// 2. 本地 60s 滑动窗口 RPM 软/硬保护：新会话与溢出遵循软硬阈值分散负载；已命中的粘性续接轮在未触及硬上限前保持粘性
	if hardLimited {
		score -= 500_000 + float64(rpm60)*1000
	} else if !stickyEligible && p.smart.softRPM > 0 && rpm60 >= p.smart.softRPM {
		score -= 50_000 + float64(rpm60-p.smart.softRPM)*5000
	} else {
		score -= float64(rpm60) * 15
	}

	// 3 & 4. 续接粘性 vs 新会话分摊：
	// - 命中粘性的续接轮：只要原号健康、未达硬 RPM 且有并发余量，优先留在原号吃隐式缓存，不因其他并发请求路过而被踢开
	// - 无粘性的新会话（如并发拉起的多个新 Sub-agent）：绝对优先完全空闲账户 (active == 0)，自然分摊到不同账号
	if stickyEligible {
		score += 100_000 - float64(account.active)*1_000
	} else if account.active == 0 {
		score += 20_000
	} else {
		score -= float64(account.active) * 20_000
	}

	// 5. 权益等级与滚动套餐额度优先
	switch account.BenefitTier {
	case BenefitTierUltra:
		score += 80
	case BenefitTierPro:
		score += 60
	case BenefitTierPlus:
		score += 40
	}
	if st.hasRollingQuota {
		score += 30
	}

	// 6. 每日请求数与 Token 均衡
	score -= float64(st.dailyRequests) * 2.0
	score -= float64(st.dailyTokens) / 200_000.0

	// 7. 历史健康惩罚分
	score -= penalty * 20.0

	// 8. ACCOUNT_ORDER 轻量加权作为同分打破者
	if rank := p.accountRankLocked(account.ID); rank < 100 {
		score += float64(100-rank) * 0.05
	}

	return score
}
