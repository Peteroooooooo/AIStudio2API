package app

import (
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestHedgeSelectionsPreferSameAccountForStickyContinuation(t *testing.T) {
	model := aistudio.Model{ID: "gemini-2.5-pro", Methods: []string{"generateContent"}, Capabilities: map[string]bool{"chat_model": true}}
	accounts := []*aistudio.Account{
		{ID: "a@example.com", BenefitTier: aistudio.BenefitTierPro, Config: aistudio.AccountConfig{Enabled: true}, State: aistudio.AccountReady, Models: []aistudio.Model{model}},
		{ID: "b@example.com", BenefitTier: aistudio.BenefitTierPro, Config: aistudio.AccountConfig{Enabled: true}, State: aistudio.AccountReady, Models: []aistudio.Model{model}},
	}
	pool := aistudio.NewAccountPool(accounts, 2)
	pool.SetUpstreamChannels([]aistudio.Channel{aistudio.ChannelPlayground})
	pool.SetRoutingStrategy("smart")

	svc := &trackedService{pool: pool}
	attempted := map[string]struct{}{"a@example.com": {}}

	// 新会话无粘性：优先跨号 hedge [other, same]
	newSel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "new-session"}
	gotNew := svc.hedgeSelections(newSel, "a@example.com", false, attempted)
	if len(gotNew) != 2 || gotNew[0].AccountID != "" || gotNew[1].AccountID != "a@example.com" {
		t.Fatalf("new session hedge order = %+v, want [other, same]", gotNew)
	}

	// 粘性续接（TurnAccountID 或已确认的 SessionKey）：优先同号备用槽位 [same, other] 保缓存
	stickySel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", TurnAccountID: "a@example.com"}
	gotSticky := svc.hedgeSelections(stickySel, "a@example.com", false, attempted)
	if len(gotSticky) != 2 || gotSticky[0].AccountID != "a@example.com" || gotSticky[1].AccountID != "" {
		t.Fatalf("sticky continuation hedge order = %+v, want [same, other]", gotSticky)
	}

	// 会话粘性同理
	pool.ConfirmSmartSession("warm-session", "a@example.com", "gemini-2.5-pro")
	sessSel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "warm-session"}
	gotSess := svc.hedgeSelections(sessSel, "a@example.com", false, attempted)
	if len(gotSess) != 2 || gotSess[0].AccountID != "a@example.com" || gotSess[1].AccountID != "" {
		t.Fatalf("session sticky hedge order = %+v, want [same, other]", gotSess)
	}
}
