//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"blackwatch/internal/domain"
	"blackwatch/internal/models"
)

func TestIntegrationEconomyXPAndLevelUps(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo, err := NewMatchPostgres(ctx, db, 100)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	econ := NewEconomyPostgres(db)

	pid, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "XP_Hero"), uniqueName(t, "disc-xp"))
	if err != nil {
		t.Fatalf("CreatePlayerWithDiscord: %v", err)
	}

	// Level 1 -> 2 costs 150 XP (reward +50 coins).
	res, err := econ.GrantXP(ctx, pid, 150, 0, 5)
	if err != nil {
		t.Fatalf("GrantXP 1->2: %v", err)
	}
	if res.OldLevel != 1 || res.NewLevel != 2 || res.CoinsAwarded != 50 || res.NewBalance != 150 {
		t.Fatalf("unexpected L2 result: %+v", res)
	}

	// Jump straight to level 5: rewards for L3(60) + L4(70) + L5(80+75=155) = 285 coins.
	xpToL5 := domain.TotalXPForLevel(5) - res.NewXP
	res2, err := econ.GrantXP(ctx, pid, xpToL5, 300, 0)
	if err != nil {
		t.Fatalf("GrantXP 2->5: %v", err)
	}
	if res2.NewLevel != 5 || res2.CoinsAwarded != 285 || res2.NewBalance != 435 {
		t.Fatalf("unexpected L5 result: %+v", res2)
	}

	pe, err := econ.GetPlayerEconomy(ctx, pid)
	if err != nil {
		t.Fatalf("GetPlayerEconomy: %v", err)
	}
	if pe.VoiceSeconds != 300 || pe.MessagesCount != 5 || pe.Level != 5 || pe.Points != 435 {
		t.Fatalf("unexpected PlayerEconomy: %+v", pe)
	}

	// Verify idempotent GrantCoins with ref_key does not double-pay.
	c1, bal1, applied1, err := econ.GrantCoins(ctx, pid, 40, domain.LedgerReasonMatchReward, "match:999", "Win #999")
	if err != nil || !applied1 || c1 != 40 || bal1 != 475 {
		t.Fatalf("first GrantCoins = (%d, %d, %v, %v)", c1, bal1, applied1, err)
	}
	c2, bal2, applied2, err := econ.GrantCoins(ctx, pid, 40, domain.LedgerReasonMatchReward, "match:999", "Win #999")
	if err != nil || applied2 || c2 != 0 || bal2 != 475 {
		t.Fatalf("duplicate GrantCoins = (%d, %d, %v, %v)", c2, bal2, applied2, err)
	}
}

func TestIntegrationDailyTipAndLeaderboards(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo, err := NewMatchPostgres(ctx, db, 100)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	econ := NewEconomyPostgres(db)

	p1, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Rich_One"), uniqueName(t, "disc-rich1"))
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	p2, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Rich_Two"), uniqueName(t, "disc-rich2"))
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}

	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	d1, err := econ.ClaimDaily(ctx, p1, t0)
	if err != nil || d1.Streak != 1 || d1.CoinsAwarded != 25 || d1.NewBalance != 125 {
		t.Fatalf("daily #1 = (%+v, %v)", d1, err)
	}

	// Immediate second claim must fail with ErrDailyAlreadyClaimed.
	if _, err := econ.ClaimDaily(ctx, p1, t0.Add(2*time.Hour)); !errors.Is(err, domain.ErrDailyAlreadyClaimed) {
		t.Fatalf("daily inside cooldown err = %v, want ErrDailyAlreadyClaimed", err)
	}

	// Claim after 24h increments streak to 2 (+30 coins).
	d2, err := econ.ClaimDaily(ctx, p1, t0.Add(24*time.Hour))
	if err != nil || d2.Streak != 2 || d2.CoinsAwarded != 30 || d2.NewBalance != 155 {
		t.Fatalf("daily #2 = (%+v, %v)", d2, err)
	}

	// Claim after 72h resets streak back to 1 (+25 coins).
	d3, err := econ.ClaimDaily(ctx, p1, t0.Add(96*time.Hour))
	if err != nil || d3.Streak != 1 || d3.CoinsAwarded != 25 || d3.NewBalance != 180 {
		t.Fatalf("daily #3 = (%+v, %v)", d3, err)
	}

	// Tip 100 from p1 to p2 (5 burned -> p2 gets +95).
	tip, err := econ.TipCoins(ctx, p1, p2, 100, 5)
	if err != nil {
		t.Fatalf("TipCoins: %v", err)
	}
	if tip.SenderBalance != 80 || tip.ReceiverBalance != 195 || tip.AmountReceived != 95 {
		t.Fatalf("unexpected TipResult: %+v", tip)
	}
	if _, err := econ.TipCoins(ctx, p1, p1, 10, 0); !errors.Is(err, domain.ErrSelfTip) {
		t.Fatalf("self tip err = %v, want ErrSelfTip", err)
	}

	// Grant XP to p1 so p1 clearly outranks p2 on the XP leaderboard.
	if _, err := econ.GrantXP(ctx, p1, 5000, 120, 10); err != nil {
		t.Fatalf("GrantXP p1: %v", err)
	}
	rich, err := econ.GetRichLeaderboard(ctx, 20)
	if err != nil || len(rich) < 2 {
		t.Fatalf("GetRichLeaderboard: %v, %+v", err, rich)
	}
	xpBoard, err := econ.GetXPLeaderboard(ctx, 20)
	if err != nil || len(xpBoard) < 2 {
		t.Fatalf("GetXPLeaderboard: %v, %+v", err, xpBoard)
	}
	posP1, posP2 := -1, -1
	for idx, row := range xpBoard {
		if row.PlayerID == p1 {
			posP1 = idx
		}
		if row.PlayerID == p2 {
			posP2 = idx
		}
	}
	if posP1 < 0 || posP2 < 0 || posP1 >= posP2 {
		t.Errorf("expected p1 (%d) ahead of p2 (%d) in XP board; positions=(%d, %d)", p1, p2, posP1, posP2)
	}
}

func TestIntegrationDiscordBettingWithInsuranceAndRefunds(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo, err := NewMatchPostgres(ctx, db, 100)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)
	econ := NewEconomyPostgres(db)

	// Create 2 roster players + 2 pure-Discord spectators (no tg_id at all!).
	capA, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Cap_A"), uniqueName(t, "disc-capa"))
	if err != nil {
		t.Fatalf("capA: %v", err)
	}
	capB, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Cap_B"), uniqueName(t, "disc-capb"))
	if err != nil {
		t.Fatalf("capB: %v", err)
	}
	specWin, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Spec_Win"), uniqueName(t, "disc-specw"))
	if err != nil {
		t.Fatalf("specWin: %v", err)
	}
	specIns, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Spec_Ins"), uniqueName(t, "disc-speci"))
	if err != nil {
		t.Fatalf("specIns: %v", err)
	}

	// Give specIns extra coins and buy 1 bet insurance coupon from seeded shop.
	if _, _, _, err := econ.GrantCoins(ctx, specIns, 100, domain.LedgerReasonAdminGrant, "", "seed"); err != nil {
		t.Fatalf("seed coins: %v", err)
	}
	items, err := econ.ListShopItems(ctx, false)
	if err != nil {
		t.Fatalf("ListShopItems: %v", err)
	}
	var insItemID int
	for _, it := range items {
		if it.Code == "bet_insurance" {
			insItemID = it.ID
		}
	}
	if insItemID == 0 {
		t.Fatal("seeded bet_insurance item not found")
	}
	if _, err := econ.PurchaseShopItem(ctx, specIns, insItemID, ""); err != nil {
		t.Fatalf("buy insurance: %v", err)
	}

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID:    "g1",
		CaptainAID: capA,
		CaptainBID: capB,
		TeamAIDs:   []int{capA},
		TeamBIDs:   []int{capB},
	})
	if err != nil {
		t.Fatalf("lobby.Create: %v", err)
	}
	if err := lobby.OpenBetting(ctx, matchID, 5*time.Minute); err != nil {
		t.Fatalf("OpenBetting: %v", err)
	}

	// Roster member cannot bet.
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: capA, TeamChosen: domain.TeamA, Amount: 10,
	}); !errors.Is(err, domain.ErrBettingOnOwnMatch) {
		t.Fatalf("roster bet err = %v, want ErrBettingOnOwnMatch", err)
	}

	// specWin bets 40 on Team A; specIns bets 60 on Team B WITH insurance.
	// specIns started at 100 + 100 seed - 75 insurance = 125 -> after 60 bet = 65.
	if _, bal, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: specWin, TeamChosen: domain.TeamA, Amount: 40,
	}); err != nil || bal != 60 {
		t.Fatalf("specWin bet = (bal=%d, err=%v)", bal, err)
	}
	if _, bal, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: specIns, TeamChosen: domain.TeamB, Amount: 60, UseInsurance: true,
	}); err != nil || bal != 65 {
		t.Fatalf("specIns bet = (bal=%d, err=%v)", bal, err)
	}

	// Duplicate bet rejected.
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: specWin, TeamChosen: domain.TeamB, Amount: 10,
	}); !errors.Is(err, domain.ErrAlreadyBet) {
		t.Fatalf("dup bet err = %v, want ErrAlreadyBet", err)
	}

	// Settle: Team A wins -> specWin takes full 100 pool (bal 60+100=160), specIns gets 50% insurance refund (+30 -> bal 95).
	summary, err := bets.PayoutWinnersDetailed(ctx, matchID, domain.TeamA)
	if err != nil {
		t.Fatalf("PayoutWinnersDetailed: %v", err)
	}
	if summary.PlayerPayouts[specWin] != 100 || summary.InsuranceRefunds[specIns] != 30 {
		t.Fatalf("unexpected PayoutSummary: %+v", summary)
	}
	winBal, _ := bets.GetPlayerPointsByID(ctx, specWin)
	insBal, _ := bets.GetPlayerPointsByID(ctx, specIns)
	if winBal != 160 || insBal != 95 {
		t.Fatalf("balances after payout: specWin=%d (want 160), specIns=%d (want 95)", winBal, insBal)
	}

	// Test match cancellation refund restores BOTH coins AND insurance coupon (95 - 75 insurance = 20).
	if _, err := econ.PurchaseShopItem(ctx, specIns, insItemID, ""); err != nil {
		t.Fatalf("buy 2nd insurance: %v", err)
	}
	matchID2, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "g1", CaptainAID: capA, CaptainBID: capB, TeamAIDs: []int{capA}, TeamBIDs: []int{capB},
	})
	if err != nil {
		t.Fatalf("lobby.Create #2: %v", err)
	}
	_ = lobby.OpenBetting(ctx, matchID2, 5*time.Minute)
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID2, PlayerID: specIns, TeamChosen: domain.TeamA, Amount: 20, UseInsurance: true,
	}); err != nil {
		t.Fatalf("insured bet #2: %v", err)
	}
	refunded, err := bets.RefundAllBets(ctx, matchID2)
	if err != nil || refunded != 1 {
		t.Fatalf("RefundAllBets = (%d, %v)", refunded, err)
	}
	peAfter, err := econ.GetPlayerEconomy(ctx, specIns)
	if err != nil {
		t.Fatalf("GetPlayerEconomy after refund: %v", err)
	}
	if peAfter.Points != 20 || peAfter.BetInsuranceCount != 1 {
		t.Fatalf("after refund: points=%d (want 20), insurance=%d (want 1)", peAfter.Points, peAfter.BetInsuranceCount)
	}
}

func TestIntegrationShopCatalogPurchasesAndPrizeApproval(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo, err := NewMatchPostgres(ctx, db, 100)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	econ := NewEconomyPostgres(db)

	buyer, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Shop_Buyer"), uniqueName(t, "disc-buyer"))
	if err != nil {
		t.Fatalf("create buyer: %v", err)
	}
	// Give buyer 2000 coins total (100 default + 1900).
	if _, _, _, err := econ.GrantCoins(ctx, buyer, 1900, domain.LedgerReasonAdminGrant, "", "seed"); err != nil {
		t.Fatalf("seed buyer: %v", err)
	}

	items, err := econ.ListShopItems(ctx, false)
	if err != nil {
		t.Fatalf("ListShopItems: %v", err)
	}
	byCode := make(map[string]models.ShopItem, len(items))
	for _, it := range items {
		byCode[it.Code] = it
	}

	// Buy custom title (750) + gold profile frame (400) -> 850 left.
	orderTitle, err := econ.PurchaseShopItem(ctx, buyer, byCode["custom_title"].ID, "Легенда Валгаллы")
	if err != nil || orderTitle.Status != domain.ShopOrderFulfilled {
		t.Fatalf("buy title = (%+v, %v)", orderTitle, err)
	}
	orderColor, err := econ.PurchaseShopItem(ctx, buyer, byCode["color_gold"].ID, "")
	if err != nil || orderColor.Status != domain.ShopOrderFulfilled {
		t.Fatalf("buy color = (%+v, %v)", orderColor, err)
	}

	pe, _ := econ.GetPlayerEconomy(ctx, buyer)
	if pe.ProfileTitle != "Легенда Валгаллы" || pe.ProfileColor != 0xF1C40F || pe.Points != 850 {
		t.Fatalf("unexpected profile cosmetics/balance: %+v", pe)
	}

	// Add limited-stock prize item (stock=1, price=300).
	stock := 1
	prize, err := econ.UpsertShopItem(ctx, &models.ShopItem{
		Code:        "mlbb_diamonds_250",
		Name:        "250 алмазов MLBB",
		Description: "Ручная выдача от админа",
		Kind:        domain.ShopKindPrize,
		Price:       300,
		Stock:       &stock,
		Enabled:     true,
		SortOrder:   50,
	})
	if err != nil {
		t.Fatalf("UpsertShopItem prize: %v", err)
	}

	o1, err := econ.PurchaseShopItem(ctx, buyer, prize.ID, "12345678 (1234)")
	if err != nil || o1.Status != domain.ShopOrderPending || o1.NewBalance != 550 {
		t.Fatalf("buy prize #1 = (%+v, %v)", o1, err)
	}
	// Out of stock now!
	if _, err := econ.PurchaseShopItem(ctx, buyer, prize.ID, "again"); !errors.Is(err, domain.ErrShopOutOfStock) {
		t.Fatalf("expected ErrShopOutOfStock, got %v", err)
	}

	// Admin cancels -> refunds 300 coins and restores stock to 1.
	cancelled, err := econ.ResolveShopOrder(ctx, o1.ID, false, "admin-1")
	if err != nil || cancelled.Status != domain.ShopOrderCancelled || cancelled.NewBalance != 850 {
		t.Fatalf("cancel order = (%+v, %v)", cancelled, err)
	}
	if _, err := econ.ResolveShopOrder(ctx, o1.ID, true, "admin-1"); !errors.Is(err, domain.ErrShopOrderAlreadyResolved) {
		t.Fatalf("re-resolve err = %v, want ErrShopOrderAlreadyResolved", err)
	}

	// Buy again now that stock was restored, then approve!
	o2, err := econ.PurchaseShopItem(ctx, buyer, prize.ID, "12345678 (1234)")
	if err != nil || o2.Status != domain.ShopOrderPending {
		t.Fatalf("buy prize #2 = (%+v, %v)", o2, err)
	}
	approved, err := econ.ResolveShopOrder(ctx, o2.ID, true, "admin-1")
	if err != nil || approved.Status != domain.ShopOrderFulfilled {
		t.Fatalf("approve order = (%+v, %v)", approved, err)
	}

	// Verify MergePlayers preserves XP/voice/insurance/ledger cleanly.
	phantomID, err := matchRepo.EnsurePlayerExists(ctx, uniqueName(t, "Shop_Buyer_Alt"))
	if err != nil {
		t.Fatalf("EnsurePlayerExists phantom: %v", err)
	}
	if _, err := econ.GrantXP(ctx, phantomID, 250, 180, 4); err != nil {
		t.Fatalf("GrantXP phantom: %v", err)
	}
	if _, err := matchRepo.MergePlayers(ctx, phantomID, buyer); err != nil {
		t.Fatalf("MergePlayers: %v", err)
	}
	merged, err := econ.GetPlayerEconomy(ctx, buyer)
	if err != nil {
		t.Fatalf("GetPlayerEconomy after merge: %v", err)
	}
	if merged.XP != 250 || merged.VoiceSeconds != 180 || merged.MessagesCount != 4 || merged.Level < 2 {
		t.Fatalf("merged economy unexpected: %+v", merged)
	}
	_ = fmt.Sprintf("verified %d", merged.PlayerID)
}

func TestIntegrationSafeguardsCancelBetRevertMatchRefundAndModeration(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	matchRepo, err := NewMatchPostgres(ctx, db, 100)
	if err != nil {
		t.Fatalf("NewMatchPostgres: %v", err)
	}
	lobby := NewLobbyMatchPostgres(db)
	bets := NewBetPostgres(db)
	econ := NewEconomyPostgres(db)
	bans := NewQueueBanPostgres(db)

	capA, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "RevA"), uniqueName(t, "disc-reva"))
	if err != nil {
		t.Fatalf("capA: %v", err)
	}
	capB, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "RevB"), uniqueName(t, "disc-revb"))
	if err != nil {
		t.Fatalf("capB: %v", err)
	}
	bettor1, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Bet1"), uniqueName(t, "disc-bet1"))
	if err != nil {
		t.Fatalf("bettor1: %v", err)
	}
	bettor2, err := matchRepo.CreatePlayerWithDiscord(ctx, uniqueName(t, "Bet2"), uniqueName(t, "disc-bet2"))
	if err != nil {
		t.Fatalf("bettor2: %v", err)
	}

	// 1. Test /queue_unban
	if err := bans.BanPlayer(ctx, "disc-unban-test", "typo", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("BanPlayer: %v", err)
	}
	if removed, err := bans.UnbanPlayer(ctx, "disc-unban-test"); err != nil || !removed {
		t.Fatalf("UnbanPlayer = (%v, %v), want (true, nil)", removed, err)
	}
	if isB, _ := bans.IsBanned(ctx, "disc-unban-test"); isB {
		t.Fatal("player still banned after UnbanPlayer")
	}

	// 2. Test player cancelling their own bet while window is open, then re-betting.
	items, _ := econ.ListShopItems(ctx, false)
	var insItemID, titleItemID int
	for _, it := range items {
		if it.Code == "bet_insurance" {
			insItemID = it.ID
		}
		if it.Code == "custom_title" {
			titleItemID = it.ID
		}
	}
	// Buy 1 insurance for bettor2 (starts at 100 -> 25 left, 1 insurance) + seed +75 so both start at 100.
	if _, err := econ.PurchaseShopItem(ctx, bettor2, insItemID, ""); err != nil {
		t.Fatalf("buy insurance: %v", err)
	}
	if _, _, _, err := econ.GrantCoins(ctx, bettor2, 75, domain.LedgerReasonAdminGrant, "", "topup"); err != nil {
		t.Fatalf("topup bettor2: %v", err)
	}

	matchID, err := lobby.Create(ctx, models.CreateLobbyMatchRequest{
		GuildID: "g1", CaptainAID: capA, CaptainBID: capB, TeamAIDs: []int{capA}, TeamBIDs: []int{capB},
	})
	if err != nil {
		t.Fatalf("lobby.Create: %v", err)
	}
	_ = lobby.OpenBetting(ctx, matchID, 5*time.Minute)

	// bettor2 accidentally bets 50 insured on Team A, cancels it, gets 50 + insurance back, then bets 50 insured on Team B!
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: bettor2, TeamChosen: domain.TeamA, Amount: 50, UseInsurance: true,
	}); err != nil {
		t.Fatalf("bettor2 accidental bet: %v", err)
	}
	refunded, restoredIns, balAfterCancel, err := bets.CancelDiscordBet(ctx, matchID, bettor2)
	if err != nil || refunded != 50 || !restoredIns || balAfterCancel != 100 {
		t.Fatalf("CancelDiscordBet = (%d, %v, %d, %v)", refunded, restoredIns, balAfterCancel, err)
	}
	// Now place the intended bets: bettor1 50 on Team A, bettor2 50 insured on Team B.
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: bettor1, TeamChosen: domain.TeamA, Amount: 50,
	}); err != nil {
		t.Fatalf("bettor1 bet: %v", err)
	}
	if _, _, err := bets.PlaceDiscordBet(ctx, models.PlaceDiscordBetRequest{
		MatchID: matchID, PlayerID: bettor2, TeamChosen: domain.TeamB, Amount: 50, UseInsurance: true,
	}); err != nil {
		t.Fatalf("bettor2 re-bet: %v", err)
	}

	// 3. Mistakenly close match for Team A: update MMR, award match coins, settle bets.
	okClose, err := lobby.AtomicSetWinner(ctx, matchID, domain.TeamA)
	if err != nil || !okClose {
		t.Fatalf("AtomicSetWinner: (%v, %v)", okClose, err)
	}
	_ = lobby.CloseBetting(ctx, matchID)
	// Cannot cancel bet after window closed.
	if _, _, _, err := bets.CancelDiscordBet(ctx, matchID, bettor1); !errors.Is(err, domain.ErrBettingClosed) {
		t.Fatalf("CancelDiscordBet after close err = %v, want ErrBettingClosed", err)
	}
	if err := lobby.UpdatePlayerMMRWithBreakdown(ctx, capA, 1025, matchID, 25, 0, 0); err != nil {
		t.Fatalf("update MMR capA: %v", err)
	}
	if err := lobby.UpdatePlayerMMRWithBreakdown(ctx, capB, 975, matchID, -25, 0, 0); err != nil {
		t.Fatalf("update MMR capB: %v", err)
	}
	if _, _, _, err := econ.GrantCoins(ctx, capA, 25, domain.LedgerReasonMatchReward, fmt.Sprintf("match:%d", matchID), "win"); err != nil {
		t.Fatalf("grant capA win coins: %v", err)
	}
	if _, _, _, err := econ.GrantCoins(ctx, capB, 10, domain.LedgerReasonMatchReward, fmt.Sprintf("match:%d", matchID), "loss"); err != nil {
		t.Fatalf("grant capB loss coins: %v", err)
	}
	if _, err := bets.PayoutWinnersDetailed(ctx, matchID, domain.TeamA); err != nil {
		t.Fatalf("PayoutWinnersDetailed: %v", err)
	}

	// 4. Admin runs /revert_match -> everything cleanly undone & match returns to ACTIVE!
	rev, err := econ.RevertFinishedMatch(ctx, matchID)
	if err != nil {
		t.Fatalf("RevertFinishedMatch: %v", err)
	}
	if rev.OldWinner != domain.TeamA || rev.RevertedMMRCount != 2 || rev.RevertedMatchCoins != 35 || rev.RevertedBetsCount != 2 {
		t.Fatalf("unexpected MatchRevertSummary: %+v", rev)
	}
	if rev.RestoredMMRs[capA] != 1000 || rev.RestoredMMRs[capB] != 1000 {
		t.Fatalf("RestoredMMRs = %+v, want both 1000", rev.RestoredMMRs)
	}

	peA, _ := econ.GetPlayerEconomy(ctx, capA)
	peB, _ := econ.GetPlayerEconomy(ctx, capB)
	peBet1, _ := econ.GetPlayerEconomy(ctx, bettor1)
	peBet2, _ := econ.GetPlayerEconomy(ctx, bettor2)
	if peA.Points != 100 || peB.Points != 100 {
		t.Fatalf("match players balances after revert: capA=%d capB=%d, want both 100", peA.Points, peB.Points)
	}
	if peBet1.Points != 100 || peBet2.Points != 100 || peBet2.BetInsuranceCount != 1 {
		t.Fatalf("bettors after revert: bet1=%d bet2=%d ins=%d, want (100, 100, 1)", peBet1.Points, peBet2.Points, peBet2.BetInsuranceCount)
	}

	// Match is ACTIVE again and ref_key was released so re-closing can award fresh match coins!
	reloaded, _ := lobby.GetByID(ctx, matchID)
	if reloaded.Status != models.LobbyMatchStatusActive || reloaded.Winner != "" {
		t.Fatalf("reloaded match after revert = (%q, %q), want ACTIVE/empty", reloaded.Status, reloaded.Winner)
	}
	if _, _, applied, err := econ.GrantCoins(ctx, capB, 25, domain.LedgerReasonMatchReward, fmt.Sprintf("match:%d", matchID), "re-closed win"); err != nil || !applied {
		t.Fatalf("re-award match coins after revert = (applied=%v, err=%v)", applied, err)
	}

	// 5. Test AdminRefundShopOrder on a fulfilled custom_title (750) order + DeleteShopItem + XP/Profile moderation.
	if _, _, _, err := econ.GrantCoins(ctx, bettor1, 900, domain.LedgerReasonAdminGrant, "", "seed"); err != nil {
		t.Fatalf("seed bettor1: %v", err)
	}
	titleOrd, err := econ.PurchaseShopItem(ctx, bettor1, titleItemID, "Опечатка")
	if err != nil {
		t.Fatalf("buy title: %v", err)
	}
	if err := econ.AdminSetProfileTitle(ctx, bettor1, "Исправленный титул"); err != nil {
		t.Fatalf("AdminSetProfileTitle: %v", err)
	}
	refOrd, err := econ.AdminRefundShopOrder(ctx, titleOrd.ID, "admin-1")
	if err != nil || refOrd.Status != domain.ShopOrderCancelled || refOrd.NewBalance != 1000 {
		t.Fatalf("AdminRefundShopOrder = (%+v, %v)", refOrd, err)
	}
	peAfterRef, _ := econ.GetPlayerEconomy(ctx, bettor1)
	if peAfterRef.ProfileTitle != "" {
		t.Fatalf("title after refund = %q, want empty", peAfterRef.ProfileTitle)
	}

	// DeleteShopItem: fails for item with orders, succeeds for freshly created unused item.
	if err := econ.DeleteShopItem(ctx, "custom_title"); !errors.Is(err, domain.ErrShopItemHasOrders) {
		t.Fatalf("DeleteShopItem(custom_title) err = %v, want ErrShopItemHasOrders", err)
	}
	tmpCode := uniqueName(t, "tmp_item")
	if _, err := econ.UpsertShopItem(ctx, &models.ShopItem{
		Code: tmpCode, Name: "Temp", Kind: domain.ShopKindPrize, Price: 50, Enabled: true,
	}); err != nil {
		t.Fatalf("UpsertShopItem tmp: %v", err)
	}
	if err := econ.DeleteShopItem(ctx, tmpCode); err != nil {
		t.Fatalf("DeleteShopItem tmp: %v", err)
	}

	// AdminSetLevel & AdminAdjustXP negative clamp.
	lvlRes, err := econ.AdminSetLevel(ctx, bettor1, 4)
	if err != nil || lvlRes.NewLevel != 4 {
		t.Fatalf("AdminSetLevel(4) = (%+v, %v)", lvlRes, err)
	}
	xpDown, err := econ.AdminAdjustXP(ctx, bettor1, -999999)
	if err != nil || xpDown.NewXP != 0 || xpDown.NewLevel != 1 {
		t.Fatalf("AdminAdjustXP(-999999) = (%+v, %v)", xpDown, err)
	}
}
