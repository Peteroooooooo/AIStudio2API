package app

import (
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/aistudio"
)

func TestHedgeSelectionsStayOnSameAccount(t *testing.T) {
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

	// 新会话无粘性：也只允许同号空闲槽位对冲，不跨账号并发对冲
	newSel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "new-session"}
	gotNew := svc.hedgeSelections(newSel, "a@example.com", false, attempted)
	if len(gotNew) != 1 || gotNew[0].AccountID != "a@example.com" || len(gotNew[0].AllowedAccountIDs) != 0 {
		t.Fatalf("new session hedge selection = %+v, want [same]", gotNew)
	}

	// 粘性续接（TurnAccountID 或已确认的 SessionKey）：同样只返回原账户
	stickySel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", TurnAccountID: "a@example.com"}
	gotSticky := svc.hedgeSelections(stickySel, "a@example.com", false, attempted)
	if len(gotSticky) != 1 || gotSticky[0].AccountID != "a@example.com" || len(gotSticky[0].AllowedAccountIDs) != 0 {
		t.Fatalf("sticky continuation hedge selection = %+v, want [same]", gotSticky)
	}

	// 会话粘性同理
	pool.ConfirmSmartSession("warm-session", "a@example.com", "gemini-2.5-pro")
	sessSel := aistudio.AccountSelection{ModelID: "gemini-2.5-pro", Method: "generateContent", SessionKey: "warm-session"}
	gotSess := svc.hedgeSelections(sessSel, "a@example.com", false, attempted)
	if len(gotSess) != 1 || gotSess[0].AccountID != "a@example.com" || len(gotSess[0].AllowedAccountIDs) != 0 {
		t.Fatalf("session sticky hedge selection = %+v, want [same]", gotSess)
	}
}
