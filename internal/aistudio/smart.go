package aistudio

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
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

func (t *smartTracker) recordDispatch(accountID string, sessionKey string, now time.Time) {
	st := t.accountState(accountID, now)
	t.pruneDispatches(st, now)
	st.recentDispatches = append(st.recentDispatches, now)
	if sessionKey != "" {
		t.setSession(sessionKey, accountID, now)
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
	var b strings.Builder
	b.Grow(512)
	b.WriteString(strings.TrimSpace(apiKey))
	b.WriteByte('|')
	b.WriteString(strings.TrimPrefix(strings.TrimSpace(modelID), "models/"))
	b.WriteByte('|')
	sysText := strings.TrimSpace(strings.Join(system, "\n"))
	if len(sysText) > 512 {
		sysText = sysText[:512]
	}
	b.WriteString(sysText)
	b.WriteByte('|')
	for _, content := range contents {
		if content.Role != RoleUser {
			continue
		}
		text := strings.TrimSpace(contentsText([]Content{content}))
		if text == "" {
			continue
		}
		if len(text) > 512 {
			text = text[:512]
		}
		b.WriteString(text)
		break
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:12])
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

func (p *AccountPool) sortSmartIndicesLocked(indices []int, selection AccountSelection, now time.Time) {
	if p.smart == nil {
		p.smart = newSmartTracker()
	}
	stickyID := p.smart.stickyAccount(selection.SessionKey, now)
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

	var score float64

	// 1. 403 账户级短时保护锁
	if !selection.BypassCooldown && now.Before(st.rejectionHoldUntil) {
		score -= 1_000_000
	}

	// 2. 本地 60s 滑动窗口 RPM 软/硬保护
	if p.smart.hardRPM > 0 && rpm60 >= p.smart.hardRPM {
		score -= 500_000 + float64(rpm60)*1000
	} else if p.smart.softRPM > 0 && rpm60 >= p.smart.softRPM {
		score -= 50_000 + float64(rpm60-p.smart.softRPM)*5000
	} else {
		score -= float64(rpm60) * 15
	}

	// 3. 绝对优先完全空闲账户 (active == 0)
	if account.active == 0 {
		score += 20_000
	} else {
		score -= float64(account.active) * 20_000
	}

	// 4. 会话粘性：仅在粘性账号健康、完全空闲且未触及软 RPM 上限时生效，尽量命中 Google 隐式提示词缓存
	if stickyID != "" && account.ID == stickyID && account.active == 0 && penalty < 10 &&
		(p.smart.softRPM == 0 || rpm60 < p.smart.softRPM) {
		score += 5_000
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
